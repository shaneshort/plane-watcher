package build

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// RunLog manages the per-run directory: one <step>.log per step plus a
// rolling run.json with the full event stream. Safe for one writer.
type RunLog struct {
	dir    string
	mu     sync.Mutex
	events []Event
	files  map[string]*os.File
}

func NewRunLog(root string, t time.Time) (*RunLog, error) {
	dir := filepath.Join(root, t.UTC().Format("2006-01-02-150405"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &RunLog{dir: dir, files: map[string]*os.File{}}, nil
}

func (r *RunLog) Dir() string { return r.dir }

// StepWriter returns an io.Writer that appends to <step>.log. The
// caller must not close it; RunLog.Close handles teardown.
func (r *RunLog) StepWriter(step string) (io.Writer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w, ok := r.files[step]; ok {
		return w, nil
	}
	f, err := os.OpenFile(filepath.Join(r.dir, step+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	r.files[step] = f
	return f, nil
}

// Append records an event; run.json is rewritten on Close to capture the
// final ordering.
func (r *RunLog) Append(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

type jsonEvent struct {
	Step    string `json:"step"`
	Kind    string `json:"kind"`
	At      string `json:"at"`
	Payload any    `json:"payload,omitempty"`
}

func (r *RunLog) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range r.files {
		_ = f.Close()
	}
	r.files = map[string]*os.File{}

	out := make([]jsonEvent, len(r.events))
	for i, e := range r.events {
		out[i] = jsonEvent{
			Step:    e.Step,
			Kind:    e.Kind.String(),
			At:      e.At.UTC().Format(time.RFC3339Nano),
			Payload: e.Payload,
		}
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.dir, "run.json"), body, 0o644)
}

// PruneRunLogs keeps the most recent keep directories under root (by
// directory name lex order, which equals chronological order given the
// ISO-style naming) and removes the rest.
func PruneRunLogs(root string, keep int) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	if len(dirs) <= keep {
		return nil
	}
	for _, name := range dirs[:len(dirs)-keep] {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}
