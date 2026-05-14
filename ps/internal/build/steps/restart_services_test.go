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

func TestRestartServicesRunsConfiguredCommand(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	_ = os.MkdirAll(bin, 0o755)
	trace := filepath.Join(dir, "trace.log")
	for _, name := range []string{"sshpass", "ssh"} {
		body := "#!/bin/bash\necho " + name + " \"$@\" >> " + trace + "\nexit 0\n"
		_ = os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	cfg := &config.Config{
		TargetHost:  "root@board",
		SSHPassword: "pw",
		Services: map[string]string{
			"plane-feeder": "/etc/init.d/S99plane-feeder restart",
		},
		Commands: []config.CommandMeta{
			{Name: "plane-feeder", Targets: []string{"arm"}},
			{Name: "dump-capture", Targets: []string{"arm"}},
		},
	}
	s, err := NewRestartServicesStep(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background(), func(build.Event) {}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, _ := os.ReadFile(trace)
	got := string(body)
	if !strings.Contains(got, "S99plane-feeder restart") {
		t.Errorf("expected service restart in trace: %q", got)
	}
}
