// Package forward is the Agent's default, cross-platform per-ticket enforcer
// (docs/02 §2.3, draft10): a userspace L4 TCP proxy. Each ticket is one
// listener bound to the Agent's overlay IP that accepts only allow-listed
// source IPs and relays bytes to exactly one asset ip:port. The listener's
// existence + source allowlist + fixed target IS the access-control fact.
//
// It is pure Go (net.Listen / net.Dial), so it works on Windows and Linux with
// no nftables/WFP dependency. The Agent only moves L4 bytes; it never
// terminates SSH or parses application traffic.
package forward

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

const dialTimeout = 10 * time.Second

// Forwarder implements enforcer.Enforcer in userspace.
type Forwarder struct {
	listenHost string // overlay IP to bind listeners on ("" = all interfaces)
	logger     *slog.Logger
	mu         sync.Mutex
	rules      map[string]*entry
}

type entry struct {
	ln     net.Listener
	addr   string
	cancel context.CancelFunc // closes active connections
	timer  *time.Timer        // TTL auto-revoke
}

// New constructs a Forwarder binding per-ticket listeners on listenHost (the
// Agent's NetBird overlay IP; "" binds all interfaces, e.g. in tests).
func New(listenHost string, logger *slog.Logger) *Forwarder {
	return &Forwarder{listenHost: listenHost, logger: logger, rules: map[string]*entry{}}
}

// Setup is a no-op: the userspace forwarder installs nothing.
func (f *Forwarder) Setup(context.Context) error { return nil }

// Apply opens a listener for ruleID forwarding to dstIP:dstPort, accepting only
// srcIPs (empty = accept any), auto-revoking after ttl. Idempotent.
func (f *Forwarder) Apply(_ context.Context, ruleID string, srcIPs []string, dstIP string, dstPort uint32, ttl time.Duration) (string, error) {
	if dstIP == "" || dstPort == 0 {
		return "", fmt.Errorf("forward apply: empty dst")
	}
	f.mu.Lock()
	if e, ok := f.rules[ruleID]; ok {
		f.mu.Unlock()
		return e.addr, nil // idempotent
	}
	f.mu.Unlock()

	ln, err := net.Listen("tcp", net.JoinHostPort(f.listenHost, "0"))
	if err != nil {
		return "", fmt.Errorf("forward apply: listen: %w", err)
	}
	allow := make(map[string]bool, len(srcIPs))
	for _, s := range srcIPs {
		if s != "" {
			allow[s] = true
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	dst := net.JoinHostPort(dstIP, strconv.Itoa(int(dstPort)))
	e := &entry{ln: ln, addr: ln.Addr().String(), cancel: cancel}
	go f.serve(ctx, ln, allow, dst)
	if ttl > 0 {
		e.timer = time.AfterFunc(ttl, func() { _ = f.Revoke(context.Background(), ruleID) })
	}

	f.mu.Lock()
	f.rules[ruleID] = e
	f.mu.Unlock()
	if f.logger != nil {
		f.logger.Info("forwarder up", "rule", ruleID, "listen", e.addr, "dst", dst, "srcs", len(allow))
	}
	return e.addr, nil
}

// serve accepts connections until the listener closes, filtering by source IP.
func (f *Forwarder) serve(ctx context.Context, ln net.Listener, allow map[string]bool, dst string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return // listener closed (Revoke/expiry)
		}
		if len(allow) > 0 {
			host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
			if !allow[host] {
				_ = c.Close() // source not allow-listed
				continue
			}
		}
		go f.pipe(ctx, c, dst)
	}
}

// pipe relays bytes between an accepted client and a fresh dial to the asset.
func (f *Forwarder) pipe(parent context.Context, client net.Conn, dst string) {
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	up, err := net.DialTimeout("tcp", dst, dialTimeout)
	if err != nil {
		return
	}
	defer func() { _ = up.Close() }()

	// Close both ends on revoke/expiry (parent ctx) or when either copy finishes.
	go func() {
		<-ctx.Done()
		_ = client.Close()
		_ = up.Close()
	}()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, up); done <- struct{}{} }()
	<-done // first direction to close tears down the other via the defers/cancel
}

// Revoke closes ruleID's listener and active connections (idempotent).
func (f *Forwarder) Revoke(_ context.Context, ruleID string) error {
	f.mu.Lock()
	e, ok := f.rules[ruleID]
	delete(f.rules, ruleID)
	f.mu.Unlock()
	if !ok {
		return nil
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	e.cancel()
	return e.ln.Close()
}

// ActiveRuleIDs lists currently-enforced rule_ids (sorted, for stable reconcile).
func (f *Forwarder) ActiveRuleIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.rules))
	for id := range f.rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
