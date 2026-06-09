package forward

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// echoServer accepts TCP connections and echoes bytes back; returns its port.
func echoServer(t *testing.T) (port uint32, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	pn, _ := net.LookupPort("tcp", p)
	return uint32(pn), func() { _ = ln.Close() }
}

func newF() *Forwarder { return New("127.0.0.1", slog.New(slog.NewTextHandler(io.Discard, nil))) }

func roundtrip(t *testing.T, addr, msg string) (string, error) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		return "", err
	}
	buf := make([]byte, len(msg))
	n, err := io.ReadFull(c, buf)
	return string(buf[:n]), err
}

func TestForwardRelaysToAsset(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	f := newF()
	addr, err := f.Apply(context.Background(), "tk1", []string{"127.0.0.1"}, "127.0.0.1", port, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := roundtrip(t, addr, "hello-yusui")
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if got != "hello-yusui" {
		t.Errorf("echo = %q, want hello-yusui", got)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	f := newF()
	a1, _ := f.Apply(context.Background(), "tk1", []string{"127.0.0.1"}, "127.0.0.1", port, time.Minute)
	a2, _ := f.Apply(context.Background(), "tk1", []string{"127.0.0.1"}, "127.0.0.1", port, time.Minute)
	if a1 != a2 {
		t.Errorf("re-Apply changed addr: %s vs %s", a1, a2)
	}
	if ids := f.ActiveRuleIDs(); len(ids) != 1 {
		t.Errorf("active rules = %v, want exactly [tk1]", ids)
	}
}

func TestSourceAllowlistRejectsOthers(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	f := newF()
	// allow only a bogus source, so a 127.0.0.1 client must be rejected.
	addr, err := f.Apply(context.Background(), "tk1", []string{"10.255.255.255"}, "127.0.0.1", port, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roundtrip(t, addr, "nope"); err == nil {
		t.Error("expected the disallowed source to be cut off, got a successful echo")
	}
}

func TestRevokeClosesListener(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	f := newF()
	addr, _ := f.Apply(context.Background(), "tk1", nil, "127.0.0.1", port, time.Minute)
	if _, err := roundtrip(t, addr, "x"); err != nil {
		t.Fatalf("pre-revoke roundtrip failed: %v", err)
	}
	if err := f.Revoke(context.Background(), "tk1"); err != nil {
		t.Fatal(err)
	}
	if len(f.ActiveRuleIDs()) != 0 {
		t.Error("rule still active after revoke")
	}
	if _, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		t.Error("listener still accepting after revoke")
	}
	if err := f.Revoke(context.Background(), "tk1"); err != nil { // idempotent
		t.Errorf("second revoke not idempotent: %v", err)
	}
}

func TestTTLAutoRevokes(t *testing.T) {
	port, stop := echoServer(t)
	defer stop()
	f := newF()
	addr, _ := f.Apply(context.Background(), "tk1", nil, "127.0.0.1", port, 80*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.ActiveRuleIDs()) == 0 {
			if _, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
				t.Error("listener still up after TTL expiry")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("rule not auto-revoked after TTL")
}
