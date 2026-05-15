package recipe

import (
	"fmt"
	"os"
	"path/filepath"
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

// Write rewrites the .inc atomically. Creates the parent directory if
// it does not yet exist (first-time scaffold on a fresh checkout).
func Write(path string, cmds []config.CommandMeta) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := RenderInc(cmds)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// BBFileContent is the canonical recipe body. The only thing that varies
// per-project is the package list, which lives in the generated .inc that
// this recipe `require`s. plane-feeder.init lives alongside in files/.
const BBFileContent = `SUMMARY = "Plane-watcher PS Go tools (slipstreamed from external build)"
DESCRIPTION = "Pre-built ARM Go binaries dropped into ${PWBUILD_STAGING_DIR} \
by the builder tool. See docs/plans/2026-05-14-builder-tui-design.md."
LICENSE = "CLOSED"

PWBUILD_STAGING_DIR ?= "${TOPDIR}/../build/pwbuild-staging"
FILESEXTRAPATHS:prepend := "${PWBUILD_STAGING_DIR}:"

require plane-watcher-tools-binaries.inc

# init.d script + defaults file live in files/ next to this recipe (NOT
# in the staging dir). BitBake searches FILESPATH in order: pwbuild-staging
# first (for binaries), then the default ${THISDIR}/files/ for these.
SRC_URI += "file://plane-feeder.init file://plane-feeder.default"

S = "${WORKDIR}"

inherit update-rc.d
INITSCRIPT_NAME = "plane-feeder"
# defaults <start-priority> <stop-priority>. plane-feeder depends on
# network + gpsd + chrony being up, so start late (90) and stop early (10).
INITSCRIPT_PARAMS = "defaults 90 10"

do_install() {
    install -d ${D}${bindir}
    for b in ${PWTOOLS_BINS}; do
        install -m 0755 ${WORKDIR}/$b ${D}${bindir}/$b
    done
    install -d ${D}${sysconfdir}/init.d
    install -m 0755 ${WORKDIR}/plane-feeder.init ${D}${sysconfdir}/init.d/plane-feeder
    install -d ${D}${sysconfdir}/default
    install -m 0644 ${WORKDIR}/plane-feeder.default ${D}${sysconfdir}/default/plane-feeder
}

FILES:${PN} = "${bindir}/* ${sysconfdir}/init.d/plane-feeder ${sysconfdir}/default/plane-feeder"
INSANE_SKIP:${PN} = "already-stripped buildpaths"

RDEPENDS:${PN} += "gpsd chrony"
`

// InitScriptContent is the canonical SysV init script for plane-feeder.
// Written via EnsureInitScript to files/plane-feeder.init the first time
// the recipe scaffolding runs. Never overwrites an existing file.
const InitScriptContent = `#!/bin/sh
### BEGIN INIT INFO
# Provides:          plane-feeder
# Required-Start:    $network $remote_fs $syslog
# Required-Stop:     $network $remote_fs $syslog
# Should-Start:      gpsd chronyd
# Should-Stop:       gpsd chronyd
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: Plane-watcher ADS-B feeder daemon
# Description:       Reads decoded frames from the FPGA AXI register block,
#                    serves Beast output on TCP 30005, hosts the dashboard
#                    on TCP 8080. See ps/cmd/plane-feeder/.
### END INIT INFO

DAEMON=/usr/bin/plane-feeder
NAME=plane-feeder
DESC="plane-watcher feeder"
PIDFILE=/var/run/$NAME.pid

# Per-board overrides: drop KEY=value lines into /etc/default/plane-feeder.
[ -r /etc/default/$NAME ] && . /etc/default/$NAME

DAEMON_ARGS="${DAEMON_ARGS:-}"

is_running() {
    [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null
}

start() {
    if is_running; then
        echo "$DESC already running (pid $(cat "$PIDFILE"))."
        return 0
    fi
    if [ ! -x "$DAEMON" ]; then
        echo "$DAEMON: not executable; refusing to start."
        return 5
    fi
    printf 'Starting %s: ' "$DESC"
    start-stop-daemon --start --background \
        --make-pidfile --pidfile "$PIDFILE" \
        --exec "$DAEMON" -- $DAEMON_ARGS
    rc=$?
    if [ "$rc" = 0 ]; then echo "$NAME."; else echo "failed (rc=$rc)."; fi
    return $rc
}

stop() {
    if ! is_running; then
        echo "$DESC not running."
        rm -f "$PIDFILE"
        return 0
    fi
    printf 'Stopping %s: ' "$DESC"
    start-stop-daemon --stop --quiet --pidfile "$PIDFILE" \
        --retry=TERM/10/KILL/5
    rc=$?
    rm -f "$PIDFILE"
    if [ "$rc" = 0 ]; then echo "$NAME."; else echo "failed (rc=$rc)."; fi
    return $rc
}

status() {
    if is_running; then
        echo "$NAME is running (pid $(cat "$PIDFILE"))."
        return 0
    fi
    echo "$NAME is not running."
    return 3
}

case "$1" in
    start)        start ;;
    stop)         stop ;;
    restart|force-reload)
                  stop; sleep 1; start ;;
    status)       status ;;
    *)
        echo "Usage: $0 {start|stop|restart|force-reload|status}"
        exit 2
        ;;
esac
exit $?
`

// EnsureBB writes the boilerplate .bb file to bbPath if it does not yet
// exist. Returns true if it created the file. Never overwrites an existing
// .bb so user edits (extra RDEPENDS, etc.) are preserved.
func EnsureBB(bbPath string) (bool, error) {
	if _, err := os.Stat(bbPath); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(bbPath), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(bbPath, []byte(BBFileContent), 0o644)
}

// EnsureInitScript writes the canonical plane-feeder SysV init script to
// recipeDir/files/plane-feeder.init if it does not yet exist. Returns
// true if it created the file. Never overwrites an existing file so
// per-board customisations (DAEMON_ARGS handling, etc.) are preserved.
func EnsureInitScript(recipeDir string) (bool, error) {
	path := filepath.Join(recipeDir, "files", "plane-feeder.init")
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(InitScriptContent), 0o755)
}

// DefaultsFileSourcePath returns the canonical location of
// plane-feeder.default: adjacent to the daemon's source code, so flag
// drift is caught at code review time (anyone editing main.go sees the
// defaults file in the same directory).
func DefaultsFileSourcePath(repoRoot string) string {
	return filepath.Join(repoRoot, "ps", "cmd", "plane-feeder", "plane-feeder.default")
}

// DefaultsFileRecipePath returns the location of the copy inside the
// Yocto recipe's files/ directory.
func DefaultsFileRecipePath(recipeDir string) string {
	return filepath.Join(recipeDir, "files", "plane-feeder.default")
}

// VerifyDefaultsFile returns nil if the recipe-side copy matches the
// source-of-truth in ps/cmd/plane-feeder/. A DefaultsDriftError signals
// the two are out of sync (caller should run sync-recipe --write).
func VerifyDefaultsFile(repoRoot, recipeDir string) error {
	src, err := os.ReadFile(DefaultsFileSourcePath(repoRoot))
	if err != nil {
		return fmt.Errorf("read defaults source: %w", err)
	}
	dst, err := os.ReadFile(DefaultsFileRecipePath(recipeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return &DefaultsDriftError{Reason: "recipe copy missing"}
		}
		return fmt.Errorf("read defaults recipe copy: %w", err)
	}
	if string(src) != string(dst) {
		return &DefaultsDriftError{Reason: "recipe copy differs from ps/cmd/plane-feeder/plane-feeder.default"}
	}
	return nil
}

// SyncDefaultsFile copies the source-of-truth defaults file into the
// recipe's files/ directory, creating parent dirs as needed.
func SyncDefaultsFile(repoRoot, recipeDir string) error {
	body, err := os.ReadFile(DefaultsFileSourcePath(repoRoot))
	if err != nil {
		return fmt.Errorf("read defaults source: %w", err)
	}
	dst := DefaultsFileRecipePath(recipeDir)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// DefaultsDriftError is returned by VerifyDefaultsFile when the recipe
// copy is out of sync with the source-of-truth in ps/cmd/plane-feeder/.
type DefaultsDriftError struct {
	Reason string
}

func (e *DefaultsDriftError) Error() string {
	return "plane-feeder.default out of sync: " + e.Reason + " — run: builder sync-recipe --write"
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
