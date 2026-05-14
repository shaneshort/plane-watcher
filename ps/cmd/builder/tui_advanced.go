package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/plane-watcher/plane-feeder/internal/build/config"
)

type advancedModel struct {
	cfg     *config.Config
	keys    []string
	cursor  int
	editing bool
	buf     string
	quit    bool
}

func newAdvancedModel(cfg *config.Config) *advancedModel {
	keys := []string{
		"TARGET_HOST",
		"SSHPASS_PASSWORD",
		"TARGET_IMAGE_DIR",
		"REMOTE_DIR",
		"VIVADO_SETTINGS",
		"PROJECT_DIR",
		"DEPLOY_DIR",
		"SD_MOUNT",
		"SD_DEVICE",
	}
	sort.Strings(keys)
	return &advancedModel{cfg: cfg, keys: keys}
}

func (m *advancedModel) Init() tea.Cmd { return nil }

func (m *advancedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		if m.editing {
			switch k.String() {
			case "enter":
				key := m.keys[m.cursor]
				if m.cfg.RawEnv == nil {
					m.cfg.RawEnv = map[string]string{}
				}
				m.cfg.RawEnv[key] = m.buf
				applyEnvToConfig(m.cfg)
				m.editing = false
				m.buf = ""
			case "esc":
				m.editing = false
				m.buf = ""
			case "backspace":
				if len(m.buf) > 0 {
					m.buf = m.buf[:len(m.buf)-1]
				}
			default:
				if len(k.String()) == 1 {
					m.buf += k.String()
				}
			}
			return m, nil
		}
		switch k.String() {
		case "q", "esc":
			m.quit = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.keys)-1 {
				m.cursor++
			}
		case "enter", "e":
			m.editing = true
			m.buf = m.cfg.RawEnv[m.keys[m.cursor]]
		case "w":
			_ = writeEnvOverrides(m.cfg)
		}
	}
	return m, nil
}

func (m *advancedModel) View() string {
	var b strings.Builder
	b.WriteString(headingStyle.Render("advanced — effective config"))
	b.WriteString("\n")
	b.WriteString(hintStyle.Render(m.cfg.PlaneWatcherEnv))
	b.WriteString("\n\n")
	for i, k := range m.keys {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		b.WriteString(fmt.Sprintf("%s%-20s = %s\n", cursor, k, m.cfg.RawEnv[k]))
	}
	if m.editing {
		b.WriteString("\nedit: ")
		b.WriteString(m.buf)
		b.WriteString("_\n")
	}
	b.WriteString("\n")
	b.WriteString(hintStyle.Render("[enter] edit  [w] write back to plane_watcher.env  [q] back"))
	return b.String()
}

func applyEnvToConfig(cfg *config.Config) {
	cfg.TargetHost = cfg.RawEnv["TARGET_HOST"]
	cfg.SSHPassword = cfg.RawEnv["SSHPASS_PASSWORD"]
	cfg.VivadoSettings = cfg.RawEnv["VIVADO_SETTINGS"]
	if v := cfg.RawEnv["PROJECT_DIR"]; v != "" {
		cfg.ProjectDir = v
	}
	if v := cfg.RawEnv["DEPLOY_DIR"]; v != "" {
		cfg.BuildDeployDir = v
	}
	cfg.SDMount = cfg.RawEnv["SD_MOUNT"]
	cfg.SDDevice = cfg.RawEnv["SD_DEVICE"]
	cfg.TargetImageDir = cfg.RawEnv["TARGET_IMAGE_DIR"]
	if v := cfg.RawEnv["REMOTE_DIR"]; v != "" {
		cfg.RemoteDir = v
	}
}

// writeEnvOverrides updates plane_watcher.env by appending a "# builder
// overrides" block. Existing content is preserved; the block is
// idempotent across writes (one block per write, overwritten in place).
func writeEnvOverrides(cfg *config.Config) error {
	const marker = "# >>> builder overrides — managed by the advanced pane\n"
	const endMarker = "# <<< builder overrides end\n"
	body, err := readFileOK(cfg.PlaneWatcherEnv)
	if err != nil {
		return err
	}
	if i := strings.Index(body, marker); i >= 0 {
		if j := strings.Index(body[i:], endMarker); j >= 0 {
			body = body[:i] + body[i+j+len(endMarker):]
		}
	}
	var block strings.Builder
	block.WriteString(marker)
	keys := make([]string, 0, len(cfg.RawEnv))
	for k := range cfg.RawEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&block, "%s=%q\n", k, cfg.RawEnv[k])
	}
	block.WriteString(endMarker)
	merged := strings.TrimRight(body, "\n") + "\n\n" + block.String()
	return writeFileAtomic(cfg.PlaneWatcherEnv, []byte(merged))
}

func readFileOK(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

func writeFileAtomic(path string, body []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
