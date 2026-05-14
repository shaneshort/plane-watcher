package build

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recStep struct {
	name string
	deps []string
	fn   func(ctx context.Context, emit func(Event)) error
}

func (r recStep) Name() string        { return r.name }
func (r recStep) DependsOn() []string { return r.deps }
func (r recStep) Run(ctx context.Context, emit func(Event)) error {
	if r.fn != nil {
		return r.fn(ctx, emit)
	}
	return nil
}

func collectEvents(ch <-chan Event) []Event {
	var out []Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}

func TestRunnerEmitsStartedAndFinished(t *testing.T) {
	plan := Plan{Steps: []Step{
		recStep{name: "a"},
		recStep{name: "b", deps: []string{"a"}},
	}}
	r := NewRunner(plan)
	go func() { _ = r.Run(context.Background()) }()
	events := collectEvents(r.Events)

	want := []struct {
		step string
		kind EventKind
	}{
		{"a", EventStarted}, {"a", EventFinished},
		{"b", EventStarted}, {"b", EventFinished},
	}
	if len(events) != len(want) {
		t.Fatalf("event count: got %d, want %d, events=%+v", len(events), len(want), events)
	}
	for i, w := range want {
		if events[i].Step != w.step || events[i].Kind != w.kind {
			t.Errorf("event[%d] = %s/%s, want %s/%s", i, events[i].Step, events[i].Kind, w.step, w.kind)
		}
	}
}

func TestRunnerFailedHaltsRemaining(t *testing.T) {
	myErr := errors.New("boom")
	plan := Plan{Steps: []Step{
		recStep{name: "a", fn: func(context.Context, func(Event)) error { return myErr }},
		recStep{name: "b", deps: []string{"a"}},
	}}
	r := NewRunner(plan)
	var runErr error
	done := make(chan struct{})
	go func() { runErr = r.Run(context.Background()); close(done) }()
	events := collectEvents(r.Events)
	<-done

	if !errors.Is(runErr, myErr) {
		t.Errorf("Run error: got %v, want wraps %v", runErr, myErr)
	}
	kinds := map[string]EventKind{}
	for _, e := range events {
		kinds[e.Step] = e.Kind
	}
	if kinds["a"] != EventFailed {
		t.Errorf("a should be Failed, got %s", kinds["a"])
	}
	if kinds["b"] != EventSkipped {
		t.Errorf("b should be Skipped, got %s", kinds["b"])
	}
}

func TestRunnerCancellation(t *testing.T) {
	plan := Plan{Steps: []Step{
		recStep{name: "long", fn: func(ctx context.Context, _ func(Event)) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		}},
	}}
	r := NewRunner(plan)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not honour cancellation")
	}
	events := collectEvents(r.Events)
	if len(events) == 0 || events[len(events)-1].Kind != EventCancelled {
		t.Errorf("expected final Cancelled event, got %+v", events)
	}
}
