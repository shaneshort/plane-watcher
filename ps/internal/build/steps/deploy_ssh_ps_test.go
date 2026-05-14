package steps

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

func TestDeploySSHPSStagesThenMoves(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o755)
	trace := filepath.Join(dir, "trace.log")
	for _, name := range []string{"sshpass", "scp", "ssh"} {
		body := "#!/bin/bash\necho " + name + " \"$@\" >> " + trace + "\nexit 0\n"
		_ = os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	stage := t.TempDir()
	_ = os.WriteFile(filepath.Join(stage, "plane-feeder"), []byte("x"), 0o755)

	cfg := &config.Config{
		StagingDir:  stage,
		TargetHost:  "root@board",
		SSHPassword: "pw",
		RemoteDir:   "/usr/local/bin",
		Commands:    []config.CommandMeta{{Name: "plane-feeder", Targets: []string{"arm"}}},
	}
	s, err := NewDeploySSHPSStep(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(trace)
	got := string(body)
	if !strings.Contains(got, "/tmp") {
		t.Errorf("expected /tmp staging in trace: %q", got)
	}
	if !strings.Contains(got, "/usr/local/bin/plane-feeder") {
		t.Errorf("expected atomic-mv target in trace: %q", got)
	}
}
