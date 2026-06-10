// Package overlay owns the Agent's NetBird membership (docs/02 §2.1-2.2,
// draft10): it supplies the overlay IP the per-ticket forwarders bind on and the
// status reported in heartbeats.
//
// The default is Static: NetBird is managed outside the Agent (the installer
// brings up the official daemon) and the Agent is told its listen host. The
// Netbird implementation, which manages the official NetBird daemon via its
// local gRPC API, is selected by config and requires that daemon; it is the one
// remaining draft10 piece (it cannot be built/verified without a NetBird env).
package overlay

import (
	"context"
	"fmt"
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

// Static assumes NetBird is managed outside the Agent (by the installer) and
// simply supplies the configured listen host. This is the default and the mode
// used by loopback dev/CI (YUSUI_LISTEN_HOST=127.0.0.1, no daemon to manage).
type Static struct{ Host string }

func (s Static) EnsureUp(context.Context) error { return nil }
func (s Static) ListenHost() string             { return s.Host }
func (s Static) Status() string                 { return "unmanaged" }

// New selects the overlay by kind. "static" (default) supplies listenHost;
// "netbird" (manage the official daemon via its local gRPC API) is not yet
// implemented and returns an explicit error rather than pretending to work.
func New(kind, listenHost string) (Overlay, error) {
	switch kind {
	case "", "static":
		return Static{Host: listenHost}, nil
	case "netbird":
		return nil, fmt.Errorf("overlay %q: managing the NetBird daemon is not yet implemented (draft10 follow-up; needs the NetBird daemon)", kind)
	default:
		return nil, fmt.Errorf("overlay %q: unknown kind (want static|netbird)", kind)
	}
}
