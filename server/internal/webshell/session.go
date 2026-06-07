package webshell

import (
	"bytes"
	"io"
	"log/slog"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Session is one live SSH proxy session: a server-held PTY to the asset, fanned
// out to subscribers (WebSocket attachers). v0.1 supports a single primary
// attacher; multi-attacher (observer/AI) lands in a later slice.
type Session struct {
	DBID     int64
	PubID    string
	TicketID int64
	Ruleset  *Ruleset
	Recorder *Recorder

	client *ssh.Client
	sess   *ssh.Session
	stdin  io.Writer
	logger *slog.Logger

	mu   sync.Mutex
	subs map[chan []byte]struct{}
	raw  bool

	closeOnce sync.Once
	closedCh  chan struct{}
	onClose   func(reason string)
	reason    string
}

// Subscribe returns a channel of output chunks and an unsubscribe func.
func (s *Session) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 1024)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
}

func (s *Session) broadcast(data []byte) {
	s.mu.Lock()
	subs := make([]chan []byte, 0, len(s.subs))
	for ch := range s.subs {
		subs = append(subs, ch)
	}
	s.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- data:
		case <-s.closedCh:
			return
		}
	}
}

// WriteStdin forwards bytes to the asset and records them.
func (s *Session) WriteStdin(b []byte) error {
	s.Recorder.Input(b)
	_, err := s.stdin.Write(b)
	return err
}

// Resize changes the remote PTY window.
func (s *Session) Resize(cols, rows int) error { return s.sess.WindowChange(rows, cols) }

// IsRaw reports whether the remote is in alt-screen/raw mode (filter paused).
func (s *Session) IsRaw() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raw
}

// Closed is closed when the session ends.
func (s *Session) Closed() <-chan struct{} { return s.closedCh }

// Reason returns the close reason.
func (s *Session) Reason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// Close tears down the SSH session (idempotent).
func (s *Session) Close(reason string) {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.reason = reason
		s.mu.Unlock()
		_ = s.sess.Close()
		_ = s.client.Close()
		_ = s.Recorder.Close()
		close(s.closedCh)
		if s.onClose != nil {
			s.onClose(reason)
		}
		s.logger.Info("session closed", "session", s.PubID, "reason", reason)
	})
}

func (s *Session) pumpStdout(stdout io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			s.Recorder.Output(data)
			s.scanRaw(data)
			s.broadcast(data)
		}
		if err != nil {
			s.Close("ssh closed")
			return
		}
	}
}

// scanRaw flips the raw flag on alt-screen enter/exit so the filter pauses for
// full-screen apps (vim/less/htop).
func (s *Session) scanRaw(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bytes.Contains(data, []byte("\x1b[?1049h")) || bytes.Contains(data, []byte("\x1b[?47h")) {
		s.raw = true
	}
	if bytes.Contains(data, []byte("\x1b[?1049l")) || bytes.Contains(data, []byte("\x1b[?47l")) {
		s.raw = false
	}
}
