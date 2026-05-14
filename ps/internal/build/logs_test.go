package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunLogCreatesPerRunDir(t *testing.T) {
	root := t.TempDir()
	rl, err := NewRunLog(root, time.Date(2026, 5, 14, 9, 30, 45, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewRunLog: %v", err)
	}
	want := filepath.Join(root, "2026-05-14-093045")
	if rl.Dir() != want {
		t.Errorf("Dir() = %q, want %q", rl.Dir(), want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("dir not created: %v", err)
	}
	rl.Close()
}

func TestRunLogWritesStepLogAndJSON(t *testing.T) {
	root := t.TempDir()
	rl, err := NewRunLog(root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer rl.Close()
	w, err := rl.StepWriter("vivado")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("line one\nline two\n")); err != nil {
		t.Fatal(err)
	}
	rl.Append(Event{Step: "vivado", Kind: EventStarted, At: time.Now()})
	rl.Append(Event{Step: "vivado", Kind: EventFinished, At: time.Now()})
	rl.Close()

	stepLog, err := os.ReadFile(filepath.Join(rl.Dir(), "vivado.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stepLog), "line one") {
		t.Errorf("vivado.log missing content: %q", stepLog)
	}

	runJSON, err := os.ReadFile(filepath.Join(rl.Dir(), "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var events []struct {
		Step string `json:"step"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(runJSON, &events); err != nil {
		t.Fatalf("run.json malformed: %v: %q", err, runJSON)
	}
	if len(events) != 2 || events[0].Kind != "started" || events[1].Kind != "finished" {
		t.Errorf("events: %+v", events)
	}
}

func TestRunLogPrune(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 25; i++ {
		dir := filepath.Join(root, time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC).Format("2006-01-02-150405"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneRunLogs(root, 20); err != nil {
		t.Fatalf("PruneRunLogs: %v", err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 20 {
		t.Errorf("expected 20 dirs after prune, got %d", len(entries))
	}
}
