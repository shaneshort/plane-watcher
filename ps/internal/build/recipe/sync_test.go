package recipe

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

func TestRenderIncDeterministic(t *testing.T) {
	cmds := []config.CommandMeta{
		{Name: "plane-feeder", Targets: []string{"arm"}},
		{Name: "dump-capture", Targets: []string{"arm"}},
		{Name: "collect-stats", Targets: []string{"host"}}, // not ARM, must be excluded
	}
	a := RenderInc(cmds)
	b := RenderInc(cmds)
	if a != b {
		t.Errorf("RenderInc not deterministic")
	}
}

func TestRenderIncContinuationInvariant(t *testing.T) {
	cmds := []config.CommandMeta{
		{Name: "plane-feeder", Targets: []string{"arm"}},
		{Name: "regdump", Targets: []string{"arm"}},
	}
	body := RenderInc(cmds)
	lines := strings.Split(body, "\n")

	var openIdx, closeIdx int
	for i, l := range lines {
		if strings.HasPrefix(l, "SRC_URI") {
			openIdx = i
		}
		if openIdx > 0 && i > openIdx && strings.TrimSpace(l) == `"` {
			closeIdx = i
			break
		}
	}
	if openIdx == 0 || closeIdx == 0 {
		t.Fatalf("SRC_URI block not located: %q", body)
	}
	// Invariant: continuation lines must end with backslash and no
	// trailing whitespace after it. BitBake-conventional whitespace
	// between the URI and the backslash is fine and idiomatic.
	endsWithBackslashRE := regexp.MustCompile(`\\$`)
	for i := openIdx; i < closeIdx; i++ {
		if !endsWithBackslashRE.MatchString(lines[i]) {
			t.Errorf("line %d missing trailing backslash: %q", i, lines[i])
		}
	}
	if strings.HasSuffix(lines[closeIdx], `\`) {
		t.Errorf("closing line ends with backslash: %q", lines[closeIdx])
	}
}

func TestVerifyDriftDetected(t *testing.T) {
	dir := t.TempDir()
	inc := filepath.Join(dir, "plane-watcher-tools-binaries.inc")
	if err := os.WriteFile(inc, []byte("stale content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmds := []config.CommandMeta{
		{Name: "plane-feeder", Targets: []string{"arm"}},
	}
	err := Verify(inc, cmds)
	if err == nil {
		t.Fatal("expected drift error")
	}
	var de *DriftError
	if !errors.As(err, &de) {
		t.Fatalf("expected *DriftError, got %T", err)
	}
	if len(de.Missing) == 0 && len(de.Stale) == 0 {
		t.Errorf("DriftError has no missing/stale entries: %+v", de)
	}
}

func TestVerifyOkAfterWrite(t *testing.T) {
	dir := t.TempDir()
	inc := filepath.Join(dir, "plane-watcher-tools-binaries.inc")
	cmds := []config.CommandMeta{
		{Name: "plane-feeder", Targets: []string{"arm"}},
		{Name: "dump-capture", Targets: []string{"arm"}},
	}
	if err := Write(inc, cmds); err != nil {
		t.Fatal(err)
	}
	if err := Verify(inc, cmds); err != nil {
		t.Errorf("Verify after Write: %v", err)
	}
}
