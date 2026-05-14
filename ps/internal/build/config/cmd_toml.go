package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

type cmdTOMLEntry struct {
	Targets []string `toml:"targets"`
	Service string   `toml:"service"`
}

// DiscoverCommands walks cmdRoot (e.g. ps/cmd) and combines directory
// entries with metadata from build.toml. Commands without a build.toml
// entry default to targets = ["host", "arm"]. build.toml entries that
// reference non-existent directories are a hard error (stale rot).
func DiscoverCommands(cmdRoot, buildTOMLPath string) ([]CommandMeta, error) {
	dirs, err := os.ReadDir(cmdRoot)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cmdRoot, err)
	}
	have := map[string]bool{}
	for _, d := range dirs {
		if d.IsDir() {
			have[d.Name()] = true
		}
	}

	entries := map[string]cmdTOMLEntry{}
	body, err := os.ReadFile(buildTOMLPath)
	switch {
	case err == nil:
		if _, err := toml.Decode(string(body), &entries); err != nil {
			return nil, fmt.Errorf("parse %s: %w", buildTOMLPath, err)
		}
	case errors.Is(err, fs.ErrNotExist):
		// treat absent build.toml as "no entries"; default-targets apply
	default:
		return nil, err
	}

	for name := range entries {
		if !have[name] {
			return nil, fmt.Errorf("%s lists %q but ps/cmd/%s does not exist", buildTOMLPath, name, name)
		}
	}

	var out []CommandMeta
	for name := range have {
		e, ok := entries[name]
		if !ok {
			out = append(out, CommandMeta{Name: name, Targets: []string{"host", "arm"}})
			continue
		}
		if len(e.Targets) == 0 {
			e.Targets = []string{"host", "arm"}
		}
		out = append(out, CommandMeta{Name: name, Targets: e.Targets, Service: e.Service})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ARMCommands returns the subset that cross-builds for ARM.
func ARMCommands(all []CommandMeta) []CommandMeta {
	var out []CommandMeta
	for _, c := range all {
		if c.BuildsForARM() {
			out = append(out, c)
		}
	}
	return out
}

// FilenameForCmd resolves to the build output binary name (Go convention:
// matches the directory name on linux).
func FilenameForCmd(c CommandMeta) string { return c.Name }

// CmdRootRelative returns a path under the repo root.
func CmdRootRelative(repoRoot string) string {
	return filepath.Join(repoRoot, "ps", "cmd")
}
