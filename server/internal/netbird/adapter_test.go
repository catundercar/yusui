package netbird

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testAdapter(h http.Handler) (*Adapter, func()) {
	srv := httptest.NewServer(h)
	a := New(srv.URL, "tok", slog.New(slog.NewTextHandler(io.Discard, nil)))
	return a, srv.Close
}

func TestEnsureGroupCreatesWhenAbsent(t *testing.T) {
	posted := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/groups", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]Group{{ID: "g-other", Name: "other"}})
			return
		}
		posted = true
		if got := r.Header.Get("Authorization"); got != "Token tok" {
			t.Errorf("auth header = %q", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "yusui:server-peers" {
			t.Errorf("group name = %v", body["name"])
		}
		_ = json.NewEncoder(w).Encode(Group{ID: "g-new", Name: "yusui:server-peers"})
	})
	a, done := testAdapter(mux)
	defer done()

	id, err := a.EnsureGroup(context.Background(), "yusui:server-peers")
	if err != nil {
		t.Fatal(err)
	}
	if id != "g-new" {
		t.Errorf("id = %s, want g-new", id)
	}
	if !posted {
		t.Error("expected a POST to create the group")
	}
}

func TestEnsureGroupIdempotent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/groups", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Error("must not POST when the group already exists")
		}
		_ = json.NewEncoder(w).Encode([]Group{{ID: "g1", Name: "yusui:server-peers"}})
	})
	a, done := testAdapter(mux)
	defer done()

	id, err := a.EnsureGroup(context.Background(), "yusui:server-peers")
	if err != nil {
		t.Fatal(err)
	}
	if id != "g1" {
		t.Errorf("id = %s, want g1", id)
	}
}

func TestEnsureBuiltinPolicyCreates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/policies", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode([]Policy{})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "yusui:builtin:server-to-agents" {
			t.Errorf("policy name = %v", body["name"])
		}
		_ = json.NewEncoder(w).Encode(Policy{ID: "p1", Name: "yusui:builtin:server-to-agents", Enabled: true})
	})
	a, done := testAdapter(mux)
	defer done()

	id, err := a.EnsureBuiltinPolicy(context.Background(), "yusui:builtin:server-to-agents", "g-src", []string{"g-a"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "p1" {
		t.Errorf("id = %s, want p1", id)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		want   ErrClass
	}{
		{401, ErrAuth}, {403, ErrAuth}, {409, ErrConflict}, {500, ErrTransient}, {400, ErrPermanent},
	}
	for _, c := range cases {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/groups", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(`{"message":"x"}`))
		})
		a, done := testAdapter(mux)
		_, err := a.ListGroups(context.Background())
		done()
		if err == nil {
			t.Fatalf("status %d: expected error", c.status)
		}
		if ClassOf(err) != c.want {
			t.Errorf("status %d: class = %v, want %v", c.status, ClassOf(err), c.want)
		}
	}
}
