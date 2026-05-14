package build

import (
	"context"
	"fmt"
	"time"
)

// Runner executes a Plan sequentially. Events are streamed through
// Events; the channel is closed when Run returns. Step.Run is invoked
// with a per-runner emit callback that stamps timestamps and forwards
// to the channel.
type Runner struct {
	Plan   Plan
	Events chan Event
}

func NewRunner(p Plan) *Runner {
	return &Runner{Plan: p, Events: make(chan Event, 256)}
}

// Run executes the plan. Returns the wrapped error of the first failing
// step, or context.Canceled / context.DeadlineExceeded on cancellation.
// All downstream steps after a failure are emitted as Skipped.
func (r *Runner) Run(ctx context.Context) (retErr error) {
	defer close(r.Events)
	emit := func(step string) func(Event) {
		return func(e Event) {
			e.Step = step
			if e.At.IsZero() {
				e.At = time.Now()
			}
			// Always send; channel is buffered (256). Dropping events on
			// ctx.Done would silently swallow Cancelled / Failed markers
			// — exactly the markers UIs need most when a run aborts.
			r.Events <- e
		}
	}
	for i, step := range r.Plan.Steps {
		if err := ctx.Err(); err != nil {
			r.emitRemaining(i, EventCancelled)
			return err
		}
		em := emit(step.Name())
		em(Event{Kind: EventStarted})
		err := step.Run(ctx, em)
		switch {
		case err == nil:
			em(Event{Kind: EventFinished})
		case ctx.Err() != nil:
			em(Event{Kind: EventCancelled, Payload: err})
			r.emitRemaining(i+1, EventCancelled)
			return ctx.Err()
		default:
			em(Event{Kind: EventFailed, Payload: err})
			r.emitRemaining(i+1, EventSkipped)
			return fmt.Errorf("step %s failed: %w", step.Name(), err)
		}
	}
	return nil
}

func (r *Runner) emitRemaining(start int, kind EventKind) {
	now := time.Now()
	for j := start; j < len(r.Plan.Steps); j++ {
		select {
		case r.Events <- Event{Step: r.Plan.Steps[j].Name(), Kind: kind, At: now}:
		default:
		}
	}
}
