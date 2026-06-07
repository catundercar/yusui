package httpapi

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/catundercar/yusui/server/internal/auth"
	"github.com/catundercar/yusui/server/internal/policy"
	"github.com/catundercar/yusui/server/internal/webshell"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/oklog/ulid/v2"
)

// WebShellHandler upgrades to WebSocket and bridges the browser xterm.js to the
// server-held PTY, applying the command filter on the stdin path (docs/09).
type WebShellHandler struct {
	mgr    *webshell.Manager
	engine *policy.Engine
	logger *slog.Logger
}

// NewWebShellHandler wires the terminal handler.
func NewWebShellHandler(mgr *webshell.Manager, engine *policy.Engine, logger *slog.Logger) *WebShellHandler {
	return &WebShellHandler{mgr: mgr, engine: engine, logger: logger}
}

// wsFrame is the length-prefixed JSON message (data is base64 for binary safety).
type wsFrame struct {
	T       string `json:"t"`
	Data    string `json:"data,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Token   string `json:"token,omitempty"`
	Rule    string `json:"rule,omitempty"`
	Msg     string `json:"msg,omitempty"`
	Line    string `json:"line,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Phase   string `json:"phase,omitempty"`
	Session string `json:"session,omitempty"`
}

func (h *WebShellHandler) terminal(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, ok := idParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	t, err := h.engine.Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "ticket not found")
		return
	}
	if p.Role == "requester" && t.RequesterID != p.UserID {
		writeErr(w, http.StatusForbidden, "not your ticket")
		return
	}
	if t.Status != "active" {
		writeErr(w, http.StatusConflict, "ticket is not active")
		return
	}

	// App-layer auth already gates this; skip browser Origin check for v0.1.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sess, err := h.mgr.OpenForTicket(ctx, t)
	if err != nil {
		_ = wsjson.Write(ctx, c, wsFrame{T: "error", Msg: "open session failed: " + err.Error()})
		return
	}

	source := "web"
	if r.URL.Query().Get("source") == "api" {
		source = "api" // M2c: AI will use a capability token that bakes this in
	}
	uid := p.UserID
	attID := h.mgr.AddAttacher(ctx, sess.DBID, &uid, source, p.Username, "primary")
	defer h.mgr.DetachAttacher(context.Background(), attID)

	var sendMu sync.Mutex
	send := func(v wsFrame) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return wsjson.Write(ctx, c, v)
	}
	_ = send(wsFrame{T: "state", Phase: "running", Session: sess.PubID})

	// stdout pump → client
	sub, unsub := sess.Subscribe()
	defer unsub()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sess.Closed():
				_ = send(wsFrame{T: "closed", Reason: sess.Reason()})
				_ = c.Close(websocket.StatusNormalClosure, "session closed")
				cancel()
				return
			case data, okk := <-sub:
				if !okk {
					return
				}
				_ = send(wsFrame{T: "stdout", Data: base64.StdEncoding.EncodeToString(data)})
			}
		}
	}()

	// stdin read loop with line-buffered Enter-gating command filter.
	var lineBuf []byte
	var pendMu sync.Mutex
	pending := ""
	for {
		var in wsFrame
		if err := wsjson.Read(ctx, c, &in); err != nil {
			return
		}
		switch in.T {
		case "stdin":
			raw, derr := base64.StdEncoding.DecodeString(in.Data)
			if derr != nil {
				continue
			}
			if sess.IsRaw() { // vim/less/etc: filtering paused, pass through
				_ = sess.WriteStdin(raw)
				continue
			}
			for _, b := range raw {
				switch b {
				case '\r', '\n':
					line := string(lineBuf)
					lineBuf = lineBuf[:0]
					d := sess.Ruleset.Match(line, source)
					switch d.Action {
					case "block":
						_ = sess.WriteStdin([]byte{0x15}) // ^U: clear the typed line
						_ = send(wsFrame{T: "filter_block", Rule: d.RuleID, Msg: d.Message, Line: line})
						h.mgr.RecordFilterEvent(ctx, sess.DBID, d.RuleID, string(d.Severity), "blocked", source, p.Username, line)
					case "confirm":
						tok := ulid.Make().String()
						pendMu.Lock()
						pending = tok
						pendMu.Unlock()
						_ = send(wsFrame{T: "filter_confirm", Rule: d.RuleID, Msg: d.Message, Line: line, Token: tok})
						dd, ll := d, line
						time.AfterFunc(10*time.Second, func() {
							pendMu.Lock()
							expired := pending == tok
							if expired {
								pending = ""
							}
							pendMu.Unlock()
							if expired {
								_ = sess.WriteStdin([]byte{0x15})
								h.mgr.RecordFilterEvent(context.Background(), sess.DBID, dd.RuleID, string(dd.Severity), "confirm_timeout", source, p.Username, ll)
							}
						})
					case "warn":
						_ = sess.WriteStdin([]byte("\r"))
						_ = send(wsFrame{T: "filter_warn", Rule: d.RuleID, Msg: d.Message, Line: line})
						h.mgr.RecordFilterEvent(ctx, sess.DBID, d.RuleID, string(d.Severity), "warned", source, p.Username, line)
					default:
						_ = sess.WriteStdin([]byte("\r"))
					}
				case 0x7f, 0x08: // backspace
					if len(lineBuf) > 0 {
						lineBuf = lineBuf[:len(lineBuf)-1]
					}
					_ = sess.WriteStdin([]byte{b})
				case 0x03, 0x15: // ^C / ^U reset the remote line
					lineBuf = lineBuf[:0]
					_ = sess.WriteStdin([]byte{b})
				default:
					if b >= 0x20 {
						lineBuf = append(lineBuf, b)
					}
					_ = sess.WriteStdin([]byte{b})
				}
			}
		case "resize":
			if in.Cols > 0 && in.Rows > 0 {
				_ = sess.Resize(in.Cols, in.Rows)
			}
		case "confirm_token":
			pendMu.Lock()
			match := in.Token != "" && in.Token == pending
			if match {
				pending = ""
			}
			pendMu.Unlock()
			if match {
				_ = sess.WriteStdin([]byte("\r"))
				h.mgr.RecordFilterEvent(ctx, sess.DBID, "confirm", "confirm", "confirmed", source, p.Username, "")
			}
		case "ping":
			_ = send(wsFrame{T: "pong"})
		}
	}
}
