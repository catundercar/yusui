package netbird

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

// TestLiveContract exercises the Adapter against a REAL NetBird Management API
// (docs/04 §4.12) — request shaping + idempotency against the actual contract,
// not a mock. Skipped unless NETBIRD_MGMT_URL + NETBIRD_TOKEN are set; stand up
// a local NetBird with deploy/netbird/ and mint a PAT with
// deploy/netbird/bootstrap-token.sh, then:
//
//	NETBIRD_MGMT_URL=http://<ip>:8081 NETBIRD_TOKEN=nbp_... \
//	  go test ./internal/netbird -run TestLiveContract -v
func TestLiveContract(t *testing.T) {
	base, tok := os.Getenv("NETBIRD_MGMT_URL"), os.Getenv("NETBIRD_TOKEN")
	if base == "" || tok == "" {
		t.Skip("set NETBIRD_MGMT_URL + NETBIRD_TOKEN to run the live NetBird contract test")
	}
	a := New(base, tok, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// EnsureGroup is idempotent: a second call with the same name returns the
	// same id (the yusui: name prefix is the only anchor — invariant #6).
	const gname = "yusui:test:server-peers"
	g1, err := a.EnsureGroup(ctx, gname)
	if err != nil {
		t.Fatalf("EnsureGroup: %v", err)
	}
	if g2, err := a.EnsureGroup(ctx, gname); err != nil || g2 != g1 {
		t.Fatalf("EnsureGroup not idempotent: id1=%q id2=%q err=%v", g1, g2, err)
	}

	// The permanent policy is created and idempotent.
	const pname = "yusui:test:server-to-agents"
	p1, err := a.EnsureBuiltinPolicy(ctx, pname, g1, []string{g1})
	if err != nil {
		t.Fatalf("EnsureBuiltinPolicy: %v", err)
	}
	if p2, err := a.EnsureBuiltinPolicy(ctx, pname, g1, []string{g1}); err != nil || p2 != p1 {
		t.Fatalf("EnsureBuiltinPolicy not idempotent: id1=%q id2=%q err=%v", p1, p2, err)
	}

	// Both are visible via the list endpoints under their yusui: names.
	groups, err := a.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if !containsName(len(groups), func(i int) string { return groups[i].Name }, gname) {
		t.Fatalf("group %q not found after ensure", gname)
	}
	policies, err := a.ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if !containsName(len(policies), func(i int) string { return policies[i].Name }, pname) {
		t.Fatalf("policy %q not found after ensure", pname)
	}
	t.Logf("live contract OK against %s: group=%s policy=%s", base, g1, p1)
}

func containsName(n int, at func(int) string, want string) bool {
	for i := 0; i < n; i++ {
		if at(i) == want {
			return true
		}
	}
	return false
}
