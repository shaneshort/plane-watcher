package steps

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/plane-watcher/plane-feeder/internal/build"
)

// ParseTimingReport extracts WNS / TNS / WHS and the
// "all timing constraints are met" / "Timing constraints are not met"
// state from a Vivado *_timing_summary_routed.rpt.
func ParseTimingReport(path string) (build.TimingResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return build.TimingResult{}, err
	}
	defer f.Close()

	res := build.TimingResult{ReportPath: path}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	var foundNumbers bool
	for scanner.Scan() {
		line := scanner.Text()
		trim := strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "All user specified timing constraints are met"):
			res.MetConstraints = true
		case strings.Contains(line, "Timing constraints are not met"):
			res.MetConstraints = false
		case !foundNumbers && looksLikeTimingNumbersRow(trim):
			fields := strings.Fields(trim)
			// Expect at least 5 numeric columns: WNS, TNS, _, _, WHS
			wns, errW := strconv.ParseFloat(fields[0], 64)
			tns, errT := strconv.ParseFloat(fields[1], 64)
			whs, errH := strconv.ParseFloat(fields[4], 64)
			if errW == nil && errT == nil && errH == nil {
				res.WNS, res.TNS, res.WHS = wns, tns, whs
				foundNumbers = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return build.TimingResult{}, err
	}
	if !foundNumbers {
		return build.TimingResult{}, fmt.Errorf("no timing numbers found in %s", path)
	}
	return res, nil
}

// looksLikeTimingNumbersRow returns true if line is whitespace-separated
// floats (the data row beneath the WNS/TNS header). Cheap heuristic:
// first two fields parse as floats and there are at least five.
func looksLikeTimingNumbersRow(line string) bool {
	if line == "" {
		return false
	}
	f := strings.Fields(line)
	if len(f) < 5 {
		return false
	}
	if _, err := strconv.ParseFloat(f[0], 64); err != nil {
		return false
	}
	if _, err := strconv.ParseFloat(f[1], 64); err != nil {
		return false
	}
	return true
}
