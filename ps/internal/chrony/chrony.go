package chrony

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// parseLeapSynced returns true if the chronyc tracking output contains
// "Leap status     : Normal", indicating chrony is synchronized.
func parseLeapSynced(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Leap status") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]) == "Normal"
			}
		}
	}
	return false
}

// Checker queries chrony synchronization status by executing chronyc
// against the chronyd Unix socket.
type Checker struct {
	bin     string
	sock    string
	timeout time.Duration
}

// NewChecker creates a Checker.
// bin defaults to "chronyc"; sock defaults to "/var/run/chrony/chronyd.sock".
func NewChecker(bin, sock string) *Checker {
	if bin == "" {
		bin = "chronyc"
	}
	if sock == "" {
		sock = "/var/run/chrony/chronyd.sock"
	}
	return &Checker{bin: bin, sock: sock, timeout: 2 * time.Second}
}

// IsSynced returns true if chrony reports Leap status: Normal.
// Returns false on any error (chrony not running, timeout, parse failure).
func (c *Checker) IsSynced() bool {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.bin, "-h", c.sock, "tracking").Output()
	if err != nil {
		return false
	}
	return parseLeapSynced(string(out))
}
