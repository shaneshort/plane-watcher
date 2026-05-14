package recipe

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

const (
	header = `# >>> builder generated from ps/cmd/build.toml — DO NOT EDIT BY HAND
# >>> regenerate: builder sync-recipe
`
	footer = `# <<< builder generated end
`
)

// RenderInc produces the deterministic content of
// plane-watcher-tools-binaries.inc from the ARM subset of cmds.
// Continuation-line invariant: opening line `SRC_URI = " \` and every
// `file://...` line end with a backslash as the final character, no
// whitespace before it; the closing `"` line ends without a backslash.
func RenderInc(cmds []config.CommandMeta) string {
	arm := config.ARMCommands(cmds)
	names := make([]string, 0, len(arm))
	for _, c := range arm {
		names = append(names, c.Name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("PWTOOLS_BINS = \"")
	b.WriteString(strings.Join(names, " "))
	b.WriteString("\"\n")
	b.WriteString("SRC_URI = \" \\\n")
	for _, n := range names {
		b.WriteString("file://")
		b.WriteString(n)
		b.WriteString(" \\\n")
	}
	b.WriteString("\"\n")
	b.WriteString(footer)
	return b.String()
}

// Write rewrites the .inc atomically.
func Write(path string, cmds []config.CommandMeta) error {
	body := RenderInc(cmds)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DriftError is returned by Verify when the .inc on disk does not match
// what RenderInc would emit for the given cmds.
type DriftError struct {
	Path    string
	Missing []string // entries the .inc lacks
	Stale   []string // entries in the .inc no longer in build.toml
	DiffMsg string   // multi-line human diff
}

func (e *DriftError) Error() string {
	var parts []string
	if len(e.Missing) > 0 {
		parts = append(parts, "missing in .inc: "+strings.Join(e.Missing, ", "))
	}
	if len(e.Stale) > 0 {
		parts = append(parts, "stale in .inc: "+strings.Join(e.Stale, ", "))
	}
	if len(parts) == 0 {
		parts = append(parts, "content differs from generator output")
	}
	return "recipe metadata out of sync with ps/cmd/build.toml: " + strings.Join(parts, "; ") + " — run: builder sync-recipe --write"
}

// Verify compares the checked-in .inc against expected output.
func Verify(path string, cmds []config.CommandMeta) error {
	want := RenderInc(cmds)
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read recipe inc: %w", err)
	}
	if string(got) == want {
		return nil
	}
	armNames := map[string]bool{}
	for _, c := range config.ARMCommands(cmds) {
		armNames[c.Name] = true
	}
	have := extractFileURIs(string(got))
	de := &DriftError{Path: path}
	for n := range armNames {
		if !have[n] {
			de.Missing = append(de.Missing, n)
		}
	}
	for n := range have {
		if !armNames[n] {
			de.Stale = append(de.Stale, n)
		}
	}
	sort.Strings(de.Missing)
	sort.Strings(de.Stale)
	de.DiffMsg = "want:\n" + want + "\ngot:\n" + string(got)
	return de
}

func extractFileURIs(s string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "file://") {
			continue
		}
		line = strings.TrimSuffix(line, "\\")
		line = strings.TrimSpace(line)
		out[strings.TrimPrefix(line, "file://")] = true
	}
	return out
}
