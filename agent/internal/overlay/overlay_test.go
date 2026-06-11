package overlay

import "testing"

func TestNewStatic(t *testing.T) {
	o, err := New(Config{Kind: "static", ListenHost: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := o.ListenHost(); got != "10.0.0.1" {
		t.Fatalf("ListenHost = %q, want 10.0.0.1", got)
	}
	if got := o.Status(); got != "unmanaged" {
		t.Fatalf("Status = %q, want unmanaged", got)
	}
}

func TestNewNetbirdDefaultsIface(t *testing.T) {
	o, err := New(Config{Kind: "netbird"})
	if err != nil {
		t.Fatal(err)
	}
	nb, ok := o.(*Netbird)
	if !ok {
		t.Fatalf("New(netbird) = %T, want *Netbird", o)
	}
	if nb.iface != "wt0" {
		t.Fatalf("default iface = %q, want wt0", nb.iface)
	}
	// No overlay interface in the test env → not connected, empty listen host.
	if got := o.Status(); got != "down" {
		t.Fatalf("Status = %q, want down", got)
	}
	if got := o.ListenHost(); got != "" {
		t.Fatalf("ListenHost = %q, want empty", got)
	}
}

func TestNewUnknownKind(t *testing.T) {
	if _, err := New(Config{Kind: "bogus"}); err == nil {
		t.Fatal("New(bogus) = nil error, want error")
	}
}
