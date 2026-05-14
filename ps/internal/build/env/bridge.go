package env

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
)

var ErrConfigFileMissing = errors.New("config file missing")

// allowlist of variables we preserve from the bash subprocess environment
// even though they were not added or changed by the env file.
var allowlist = map[string]struct{}{
	"PATH": {}, "HOME": {}, "USER": {}, "LANG": {},
}

// blocklist of bash-subprocess noise variables that must never appear in
// the effective config view, regardless of whether the env file mentions
// them. Set membership is checked after the diff to defend against env
// files that set e.g. PWD explicitly.
var blocklist = map[string]struct{}{
	"SHLVL": {}, "_": {}, "PWD": {}, "OLDPWD": {}, "IFS": {},
	"SHELL": {}, "TERM": {},
}

type LoadOptions struct {
	Path     string // path to plane_watcher.env
	RepoRoot string // value of REPO_ROOT to export before sourcing
	Explicit bool   // user set CONFIG_FILE; missing file is a hard error
}

// Load runs a bash subprocess that sources the env file with set -euo
// pipefail and returns the (added union changed) names plus the
// allowlist. Variables in the blocklist are dropped unconditionally.
func Load(opts LoadOptions) (map[string]string, error) {
	if _, err := os.Stat(opts.Path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if opts.Explicit {
				return nil, fmt.Errorf("%w: %s", ErrConfigFileMissing, opts.Path)
			}
			return map[string]string{}, nil
		}
		return nil, err
	}

	// The bash subprocess starts with the exact env Load sets in cmd.Env
	// below. That fixed set is the baseline; anything else in the post-
	// source env was added or changed by the env file. Filtering against
	// the cmd.Env baseline is therefore equivalent to before/after diff,
	// without the temp-file dance.
	const script = `
set -euo pipefail
export REPO_ROOT="$2"
set -a
# shellcheck disable=SC1090
source "$1"
set +a
env -0
`
	cmd := exec.Command("bash", "-c", script, "_", opts.Path, opts.RepoRoot)
	cmd.Env = []string{
		// minimal bash environment; PATH inherited so source can find files
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"USER=" + os.Getenv("USER"),
		"LANG=" + os.Getenv("LANG"),
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("env bridge failed: %v: %s", err, stderr.String())
	}

	// Baseline = the exact cmd.Env we passed in plus REPO_ROOT exported
	// by the script. A variable is "from the env file" iff its name is
	// not in this set, OR it is in the explicit allowlist below (PATH
	// etc., which the env file is allowed to mutate).
	baseline := map[string]struct{}{
		"PATH": {}, "HOME": {}, "USER": {}, "LANG": {}, "REPO_ROOT": {},
	}

	out := map[string]string{}
	parts := bytes.Split(stdout.Bytes(), []byte{0})
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		eq := bytes.IndexByte(p, '=')
		if eq < 0 {
			continue
		}
		name := string(p[:eq])
		value := string(p[eq+1:])
		if _, banned := blocklist[name]; banned {
			continue
		}
		_, inBaseline := baseline[name]
		_, inAllow := allowlist[name]
		if inBaseline && !inAllow {
			continue
		}
		out[name] = value
	}
	return out, nil
}

// MergeIntoOSEnv applies a loaded env map to os.Environ-style "K=V" pairs,
// overwriting any existing values. Useful for child processes (exec.Cmd).
func MergeIntoOSEnv(base []string, overrides map[string]string) []string {
	have := map[string]bool{}
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		name := kv[:eq]
		if v, ok := overrides[name]; ok {
			out = append(out, name+"="+v)
			have[name] = true
		} else {
			out = append(out, kv)
		}
	}
	for k, v := range overrides {
		if !have[k] {
			out = append(out, k+"="+v)
		}
	}
	return out
}
