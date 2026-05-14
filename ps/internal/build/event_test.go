package build

import (
	"testing"
	"time"
)

func TestEventKindString(t *testing.T) {
	cases := []struct {
		kind EventKind
		want string
	}{
		{EventStarted, "started"},
		{EventProgress, "progress"},
		{EventLogLine, "logline"},
		{EventWarning, "warning"},
		{EventTimingResult, "timing"},
		{EventFinished, "finished"},
		{EventFailed, "failed"},
		{EventCancelled, "cancelled"},
		{EventSkipped, "skipped"},
	}
	for _, c := range cases {
		if got := c.kind.String(); got != c.want {
			t.Errorf("EventKind(%d).String() = %q, want %q", c.kind, got, c.want)
		}
	}
}

func TestTimingResultMet(t *testing.T) {
	r := TimingResult{MetConstraints: true, WNS: 0.234, TNS: 0.0}
	ev := Event{Step: "vivado", Kind: EventTimingResult, At: time.Now(), Payload: r}
	got, ok := ev.Payload.(TimingResult)
	if !ok || !got.MetConstraints {
		t.Fatalf("payload roundtrip failed: %+v", ev)
	}
}
