package env

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "test.env")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBridgeLoadsKVs(t *testing.T) {
	p := writeEnv(t, "TARGET_HOST=root@board\nSSHPASS_PASSWORD=foo\n")
	got, err := Load(LoadOptions{Path: p, RepoRoot: "/repo", Explicit: false})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got["TARGET_HOST"] != "root@board" || got["SSHPASS_PASSWORD"] != "foo" {
		t.Errorf("missing kv: %+v", got)
	}
}

func TestBridgeExpandsRepoRoot(t *testing.T) {
	p := writeEnv(t, `IMPL_DIR=$REPO_ROOT/hdl/vivado/build`+"\n")
	got, err := Load(LoadOptions{Path: p, RepoRoot: "/repo", Explicit: false})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got["IMPL_DIR"] != "/repo/hdl/vivado/build" {
		t.Errorf("REPO_ROOT not expanded: %q", got["IMPL_DIR"])
	}
}

func TestBridgeFiltersShellNoise(t *testing.T) {
	p := writeEnv(t, "FOO=bar\n")
	got, err := Load(LoadOptions{Path: p, RepoRoot: "/repo", Explicit: false})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, banned := range []string{"SHLVL", "PWD", "OLDPWD", "_", "IFS"} {
		if _, present := got[banned]; present {
			t.Errorf("banned variable %q leaked: %q", banned, got[banned])
		}
	}
	if got["FOO"] != "bar" {
		t.Errorf("missing FOO: %+v", got)
	}
}

func TestBridgeMissingDefaultSilent(t *testing.T) {
	got, err := Load(LoadOptions{Path: "/nonexistent/path.env", RepoRoot: "/repo", Explicit: false})
	if err != nil {
		t.Fatalf("expected silent skip, got err=%v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %+v", got)
	}
}

func TestBridgeMissingExplicitFails(t *testing.T) {
	_, err := Load(LoadOptions{Path: "/nonexistent/path.env", RepoRoot: "/repo", Explicit: true})
	if !errors.Is(err, ErrConfigFileMissing) {
		t.Errorf("expected ErrConfigFileMissing, got %v", err)
	}
}

func TestBridgeSyntaxErrorFails(t *testing.T) {
	p := writeEnv(t, "FOO=bar\nthis is not valid shell &^%\n")
	_, err := Load(LoadOptions{Path: p, RepoRoot: "/repo", Explicit: false})
	if err == nil {
		t.Fatal("expected syntax error")
	}
}
