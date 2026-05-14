package steps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/plane-watcher/plane-feeder/internal/build"
	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type psStageStep struct {
	cfg *config.Config
}

func NewPSStageStep(cfg *config.Config) (build.Step, error) {
	if cfg.StagingDir == "" {
		return nil, fmt.Errorf("ps-stage: StagingDir empty")
	}
	return &psStageStep{cfg: cfg}, nil
}

func (s *psStageStep) Name() string        { return "ps-stage" }
func (s *psStageStep) DependsOn() []string { return []string{"ps-build"} }

type manifestEntry struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type manifest struct {
	BuiltAt  string                   `json:"built_at"`
	GitSHA   string                   `json:"git_sha,omitempty"`
	Binaries map[string]manifestEntry `json:"binaries"`
}

func (s *psStageStep) Run(_ context.Context, emit func(build.Event)) error {
	m := manifest{
		BuiltAt:  time.Now().UTC().Format(time.RFC3339),
		Binaries: map[string]manifestEntry{},
	}
	for _, c := range config.ARMCommands(s.cfg.Commands) {
		path := filepath.Join(s.cfg.StagingDir, c.Name)
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		m.Binaries[c.Name] = manifestEntry{SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	emit(build.Event{Kind: build.EventLogLine, Payload: fmt.Sprintf("wrote manifest with %d entries", len(m.Binaries))})
	return os.WriteFile(filepath.Join(s.cfg.StagingDir, "manifest.json"), body, 0o644)
}
