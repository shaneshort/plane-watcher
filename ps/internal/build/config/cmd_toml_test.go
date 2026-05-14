package config

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDiscoverCommandsBuildTOML(t *testing.T) {
	root := t.TempDir()
	mkdir := func(name string) { _ = os.MkdirAll(filepath.Join(root, name), 0o755) }
	mkdir("plane-feeder")
	mkdir("dump-capture")
	mkdir("collect-stats")
	mkdir("regdump")

	tomlPath := filepath.Join(root, "build.toml")
	body := `
[plane-feeder]
targets = ["arm"]
service = "S99plane-feeder"

[collect-stats]
targets = ["host"]

[dump-capture]
targets = ["arm", "host"]
`
	if err := os.WriteFile(tomlPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverCommands(root, tomlPath)
	if err != nil {
		t.Fatalf("DiscoverCommands: %v", err)
	}
	names := make([]string, len(got))
	for i, c := range got {
		names[i] = c.Name
	}
	sort.Strings(names)
	want := []string{"collect-stats", "dump-capture", "plane-feeder", "regdump"}
	if len(names) != len(want) {
		t.Fatalf("count mismatch: got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("name[%d] = %q, want %q", i, names[i], want[i])
		}
	}
	for _, c := range got {
		if c.Name == "regdump" {
			if !c.BuildsForARM() || !c.BuildsForHost() {
				t.Errorf("default targets wrong for regdump: %+v", c.Targets)
			}
		}
		if c.Name == "plane-feeder" {
			if !c.BuildsForARM() || c.BuildsForHost() {
				t.Errorf("plane-feeder targets wrong: %+v", c.Targets)
			}
			if c.Service != "S99plane-feeder" {
				t.Errorf("service hint missing: %+v", c)
			}
		}
	}
}

func TestDiscoverCommandsStaleTOMLEntry(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "plane-feeder"), 0o755)
	tomlPath := filepath.Join(root, "build.toml")
	body := `
[sweep-gain]
targets = ["arm"]
`
	if err := os.WriteFile(tomlPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := DiscoverCommands(root, tomlPath)
	if err == nil {
		t.Fatal("expected error for build.toml entry with no ps/cmd/<name> dir")
	}
}
