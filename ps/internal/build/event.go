package build

import "time"

type EventKind int

const (
	EventStarted EventKind = iota
	EventProgress
	EventLogLine
	EventWarning
	EventTimingResult
	EventFinished
	EventFailed
	EventCancelled
	EventSkipped
)

func (k EventKind) String() string {
	switch k {
	case EventStarted:
		return "started"
	case EventProgress:
		return "progress"
	case EventLogLine:
		return "logline"
	case EventWarning:
		return "warning"
	case EventTimingResult:
		return "timing"
	case EventFinished:
		return "finished"
	case EventFailed:
		return "failed"
	case EventCancelled:
		return "cancelled"
	case EventSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// TimingResult is emitted by the vivado step after parsing the routed
// timing summary report. MetConstraints == false fails the step and
// causes downstream packaging/deploy to be skipped.
type TimingResult struct {
	MetConstraints bool
	WNS, TNS, WHS  float64
	ReportPath     string
}

// ProgressPayload accompanies EventProgress events.
type ProgressPayload struct {
	Message string
	// Fraction is optional progress in [0,1]; negative if unknown.
	Fraction float64
}

type Event struct {
	Step    string
	Kind    EventKind
	At      time.Time
	Payload any
}
