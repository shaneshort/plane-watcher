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

func TestDeploySSHBuildsExpectedCommand(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(dir, "trace.log")
	for _, name := range []string{"sshpass", "scp", "ssh"} {
		body := "#!/bin/bash\necho " + name + " \"$@\" >> " + trace + "\nexit 0\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	deploy := t.TempDir()
	_ = os.WriteFile(filepath.Join(deploy, "BOOT.BIN"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(deploy, "image.ub"), []byte("x"), 0o644)

	cfg := &config.Config{
		BuildDeployDir: deploy,
		TargetHost:     "root@board",
		SSHPassword:    "pw",
		TargetImageDir: "/run/media/mmcblk0p1",
	}
	s, err := NewDeploySSHStep(cfg, build.Selections{Bitstream: true, Petalinux: true, Deploy: build.DeploySSH, Reboot: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(trace)
	got := string(body)
	if !strings.Contains(got, "BOOT.BIN") || !strings.Contains(got, "image.ub") {
		t.Errorf("expected both artefacts in trace: %q", got)
	}
	if !strings.Contains(got, "reboot") {
		t.Errorf("expected reboot invocation when Reboot=true; trace=%q", got)
	}
}
