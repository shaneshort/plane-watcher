package steps

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/build"
)

func collect(events <-chan build.Event) []build.Event {
	var out []build.Event
	for e := range events {
		out = append(out, e)
	}
	return out
}

func TestRunStreamsLogLines(t *testing.T) {
	ch := make(chan build.Event, 16)
	emit := func(e build.Event) { ch <- e }
	go func() {
		err := Run(context.Background(), RunOptions{
			Argv: []string{"bash", "-c", "echo foo; echo bar; echo baz >&2"},
			Emit: emit,
		})
		if err != nil {
			t.Errorf("Run err: %v", err)
		}
		close(ch)
	}()
	events := collect(ch)
	var lines []string
	for _, e := range events {
		if e.Kind == build.EventLogLine {
			lines = append(lines, e.Payload.(string))
		}
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"foo", "bar", "baz"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in output: %s", want, joined)
		}
	}
}

func TestRunCancelKillsProcessGroup(t *testing.T) {
	ch := make(chan build.Event, 16)
	emit := func(e build.Event) { ch <- e }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunOptions{
			Argv: []string{"bash", "-c", "sleep 30"},
			Emit: emit,
		})
		close(ch)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil error after cancel")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Run did not honour cancel")
	}
}

func TestRunNonzeroExitIsError(t *testing.T) {
	ch := make(chan build.Event, 8)
	defer close(ch)
	err := Run(context.Background(), RunOptions{
		Argv: []string{"bash", "-c", "exit 7"},
		Emit: func(e build.Event) { ch <- e },
	})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected *ExitError, got %T", err)
	}
	if ee.Code != 7 {
		t.Errorf("exit code = %d, want 7", ee.Code)
	}
}
