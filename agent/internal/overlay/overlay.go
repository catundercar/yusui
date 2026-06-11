// Package overlay owns the Agent's NetBird membership (docs/02 §2.1-2.2,
// draft10): it supplies the overlay IP the per-ticket forwarders bind on and the
// status reported in heartbeats.
//
// Two modes:
//   - Static: NetBird is managed entirely outside the Agent and the listen host
//     is configured. Default; used by loopback dev/CI (no daemon to manage).
//   - Netbird: the Agent manages the official NetBird daemon — brings the overlay
//     up with a setup key and discovers the overlay IP from the WireGuard
//     interface. draft10 prefers the daemon's local gRPC API; v1 drives the
//     `netbird` CLI (itself a client of that API) and reads the interface — the
//     direct gRPC client is a follow-up (docs/10).
package overlay

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"time"
)

// Overlay provides the Agent's forwarder listen host and overlay status.
type Overlay interface {
	// EnsureUp brings the overlay up (no-op for Static).
	EnsureUp(ctx context.Context) error
	// ListenHost is the address per-ticket forwarders bind on (the overlay IP;
	// "" binds all interfaces, e.g. loopback dev/CI).
	ListenHost() string
	// Status is reported in heartbeats: "connected" / "unmanaged" / "down".
	Status() string
}

// Config selects and parameterizes the overlay.
type Config struct {
	Kind       string // "static" (default) | "netbird"
	ListenHost string // static: the configured overlay IP ("" = all interfaces)
	Iface      string // netbird: overlay interface to read (default "wt0")
	SetupKey   string // netbird: enrollment key; empty = assume the daemon is already up
	MgmtURL    string // netbird: NetBird management URL passed to `netbird up`
	Logger     *slog.Logger
}

// New selects the overlay by Config.Kind.
func New(c Config) (Overlay, error) {
	switch c.Kind {
	case "", "static":
		return Static{Host: c.ListenHost}, nil
	case "netbird":
		iface := c.Iface
		if iface == "" {
			iface = "wt0"
		}
		return &Netbird{iface: iface, setupKey: c.SetupKey, mgmtURL: c.MgmtURL, logger: c.Logger}, nil
	default:
		return nil, fmt.Errorf("overlay %q: unknown kind (want static|netbird)", c.Kind)
	}
}

// Static assumes NetBird is managed outside the Agent (by the installer) and
// simply supplies the configured listen host. This is the default and the mode
// used by loopback dev/CI (YUSUI_LISTEN_HOST=127.0.0.1, no daemon to manage).
type Static struct{ Host string }

func (s Static) EnsureUp(context.Context) error { return nil }
func (s Static) ListenHost() string             { return s.Host }
func (s Static) Status() string                 { return "unmanaged" }

// Netbird manages the official NetBird daemon: it brings the overlay up with a
// setup key (when one is configured) and discovers the overlay IP from the
// WireGuard interface. The daemon itself is installed + run separately (docs/02
// §2.9); this only drives it and reads its interface.
type Netbird struct {
	iface    string
	setupKey string
	mgmtURL  string
	logger   *slog.Logger
}

func (n *Netbird) log() *slog.Logger {
	if n.logger != nil {
		return n.logger
	}
	return slog.Default()
}

// EnsureUp runs `netbird up` (when a setup key is set) and then waits for the
// overlay interface to acquire an IPv4. With no setup key it assumes the daemon
// is already joined and only waits for the interface.
func (n *Netbird) EnsureUp(ctx context.Context) error {
	if n.setupKey != "" {
		args := []string{"up", "--setup-key", n.setupKey}
		if n.mgmtURL != "" {
			args = append(args, "--management-url", n.mgmtURL)
		}
		// `netbird up` is idempotent — safe to re-run if already connected.
		if out, err := exec.CommandContext(ctx, "netbird", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("netbird up: %w: %s", err, out)
		}
		n.log().Info("netbird up issued", "iface", n.iface, "mgmt", n.mgmtURL)
	}
	for {
		if ip := ifaceIPv4(n.iface); ip != "" {
			n.log().Info("overlay interface up", "iface", n.iface, "ip", ip)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// ListenHost returns the overlay interface's IPv4 (where forwarders bind).
func (n *Netbird) ListenHost() string { return ifaceIPv4(n.iface) }

// Status reports "connected" when the overlay interface has an IPv4, else "down".
func (n *Netbird) Status() string {
	if ifaceIPv4(n.iface) != "" {
		return "connected"
	}
	return "down"
}

// ifaceIPv4 returns the first non-loopback IPv4 on the named interface, or "".
func ifaceIPv4(name string) string {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if v4 := ipn.IP.To4(); v4 != nil && !v4.IsLoopback() {
				return v4.String()
			}
		}
	}
	return ""
}
