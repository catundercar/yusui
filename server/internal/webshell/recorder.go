package webshell

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Recorder writes an asciinema v2 cast file (JSONL): a header line then
// [elapsed, stream, data] events. Stream "o"=output, "i"=input (docs/09 §9.5).
type Recorder struct {
	mu    sync.Mutex
	f     *os.File
	start time.Time
}

// NewRecorder creates the cast file and writes its header. Returns the recorder
// and the file URI.
func NewRecorder(dir, sessionPubID string, cols, rows int, startedAt time.Time) (*Recorder, string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, sessionPubID+".cast")
	f, err := os.Create(path)
	if err != nil {
		return nil, "", err
	}
	header := map[string]any{
		"version": 2, "width": cols, "height": rows,
		"timestamp": startedAt.Unix(),
		"env":       map[string]string{"TERM": "xterm-256color", "SHELL": "/bin/sh"},
	}
	b, _ := json.Marshal(header)
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return nil, "", err
	}
	return &Recorder{f: f, start: startedAt}, "file://" + path, nil
}

func (r *Recorder) record(stream string, data []byte) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ev := []any{time.Since(r.start).Seconds(), stream, string(data)}
	b, _ := json.Marshal(ev)
	_, _ = r.f.Write(append(b, '\n'))
}

// Output records terminal output.
func (r *Recorder) Output(data []byte) { r.record("o", data) }

// Input records forwarded keystrokes.
func (r *Recorder) Input(data []byte) { r.record("i", data) }

// Close flushes and closes the cast file.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}
