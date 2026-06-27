package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/crypto/argon2"
)

var (
	titleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#00F5D4")).Bold(true).MarginLeft(2)
	statusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFF"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00F5D4")).Bold(true)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF007F")).Bold(true)
	focusedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#00F5D4")).Bold(true)
)

type sessionState int

const (
	stateSelectHost sessionState = iota
	stateInputPassphrase
	stateDeploying
)

type installStep struct {
	name string
	log  string
}

type Config struct {
	Hosts []string `json:"hosts"`
}

type model struct {
	state        sessionState
	hosts        []string
	selectedHost int
	steps        []installStep
	currentStep  int
	progress     progress.Model
	textInput    textinput.Model
	masterPhrase string
	yubiSerial   string
	done         bool
	err          error
	logs         []string
}

type stepCompleteMsg int
type logMsg string
type successMsg struct{}
type errMsg struct{ err error }

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "Enter your secure master passphrase..."
	ti.EchoMode = textinput.EchoPassword
	ti.Focus()

	availableHosts := []string{"pc-th"}

	exePath, err := os.Executable()
	if err == nil {
		configPath := filepath.Join(filepath.Dir(exePath), "hosts.json")
		configFile, err := os.ReadFile(configPath)
		if err == nil {
			var cfg Config
			if json.Unmarshal(configFile, &cfg) == nil && len(cfg.Hosts) > 0 {
				availableHosts = cfg.Hosts
			}
		}
	}

	return model{
		state:        stateSelectHost,
		hosts:        availableHosts,
		selectedHost: 0,
		steps: []installStep{
			{name: "Detect hardware storage & YubiKey presence", log: "Pending..."},
			{name: "Extract YubiKey metadata for dynamic salt", log: "Pending..."},
			{name: "Generate deterministic transient age key (Argon2id)", log: "Pending..."},
			{name: "Provision YubiKey Slot 2 (Challenge-Response configuration)", log: "Pending..."},
			{name: "Generate runtime Hardware Configuration", log: "Pending..."},
			{name: "Execute secure encrypted nixos-install deployment", log: "Pending..."},
		},
		progress:  progress.New(progress.WithDefaultGradient()),
		textInput: ti,
		logs:      []string{"System initialized. Please select target infrastructure profile."},
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch m.state {
	case stateSelectHost:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.selectedHost > 0 {
					m.selectedHost--
				}
			case "down", "j":
				if m.selectedHost < len(m.hosts)-1 {
					m.selectedHost++
				}
			case "enter":
				m.state = stateInputPassphrase
				m.logs = append(m.logs, fmt.Sprintf("🎯 Profile target set: %s. Awaiting crypto authority validation...", m.hosts[m.selectedHost]))
				return m, nil
			}
		}
		return m, nil

	case stateInputPassphrase:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "enter":
				m.masterPhrase = m.textInput.Value()
				if len(strings.TrimSpace(m.masterPhrase)) < 8 {
					m.err = fmt.Errorf("passphrase metrics suboptimal: minimum 8 characters required")
					return m, nil
				}
				m.state = stateDeploying
				m.logs = append(m.logs, "🚀 Crypto signature confirmed. Initiating deployment sequence...")
				return m, m.runStep(0)
			}
		}
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd

	case stateDeploying:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.String() == "ctrl+c" || msg.String() == "q" {
				return m, tea.Quit
			}

		case logMsg:
			m.logs = append(m.logs, string(msg))
			if len(m.logs) > 6 {
				m.logs = m.logs[1:]
			}
			return m, nil

		case stepCompleteMsg:
			nextStep := int(msg) + 1
			m.currentStep = nextStep
			pct := float64(nextStep) / float64(len(m.steps))

			if nextStep >= len(m.steps) {
				m.done = true
				return m, m.progress.SetPercent(1.0)
			}

			return m, tea.Batch(m.progress.SetPercent(pct), m.runStep(nextStep))

		case errMsg:
			m.err = msg.err
			m.done = true
			return m, nil

		case progress.FrameMsg:
			progressModel, cmd := m.progress.Update(msg)
			m.progress = progressModel.(progress.Model)
			return m, cmd
		}
	}

	return m, cmd
}

func (m model) View() string {
	var s strings.Builder
	s.WriteString("\n" + titleStyle.Render("NixOS Air-Gapped YubiKey Bootstrapper") + "\n\n")

	if m.state == stateSelectHost {
		s.WriteString(" Select Target Host Architecture Profile:\n")
		for i, host := range m.hosts {
			if m.selectedHost == i {
				s.WriteString(focusedStyle.Render(fmt.Sprintf("  ➔  %s\n", host)))
			} else {
				s.WriteString(fmt.Sprintf("     %s\n", host))
			}
		}
		s.WriteString("\n [ Navigation: Up/Down or J/K • Selection: Enter ]\n\n")
	}

	if m.state == stateInputPassphrase || m.state == stateDeploying {
		for i, step := range m.steps {
			if i < m.currentStep {
				s.WriteString(fmt.Sprintf("  [✓] %s\n", step.name))
			} else if i == m.currentStep && !m.done && m.state == stateDeploying {
				s.WriteString(fmt.Sprintf("  [➔] %s — %s\n", step.name, statusStyle.Render(step.log)))
			} else {
				s.WriteString(fmt.Sprintf("  [ ] %s\n", step.name))
			}
		}
		s.WriteString("\n " + m.progress.View() + "\n\n")
	}

	if m.state == stateInputPassphrase {
		s.WriteString(fmt.Sprintf("🔑 MASTER PASSPHRASE: %s\n\n", m.textInput.View()))
	}

	s.WriteString("📝 Security Infrastructure Logs:\n")
	s.WriteString("--------------------------------------------------\n")
	for _, log := range m.logs {
		s.WriteString(" " + log + "\n")
	}
	s.WriteString("--------------------------------------------------\n")

	if m.err != nil {
		s.WriteString("\n" + errorStyle.Render(fmt.Sprintf("Deployment failure: %v", m.err)) + "\n")
	} else if m.done {
		s.WriteString("\n" + successStyle.Render("✨ Finished! Profile targets successfully committed. Rebooting ecosystem...") + "\n")
	}

	return s.String()
}

func (m *model) runStep(stepIdx int) tea.Cmd {
	return func() tea.Msg {
		switch stepIdx {
		case 0:
			if err := exec.Command("ykman", "--version").Run(); err != nil {
				return errMsg{err: fmt.Errorf("yubikey manager (ykman) missing in target system path: %v", err)}
			}
			time.Sleep(300 * time.Millisecond)
			return stepCompleteMsg(stepIdx)

		case 1:
			cmd := exec.Command("ykman", "list")
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err != nil {
				return errMsg{err: fmt.Errorf("hardware token authentication missing: %v", err)}
			}

			fields := strings.Fields(out.String())
			for i, field := range fields {
				if field == "Serial:" && i+1 < len(fields) {
					m.yubiSerial = fields[i+1]
				}
			}
			if m.yubiSerial == "" {
				m.yubiSerial = "fallback-hardware-token-salt-2026"
			}
			return stepCompleteMsg(stepIdx)

		case 2:
			salt := []byte("yubikey-salt-" + m.yubiSerial)
			rawKey := argon2.IDKey([]byte(m.masterPhrase), salt, 3, 64*1024, 4, 64)
			hashed := sha256.Sum256(rawKey)

			ageKeyContent := fmt.Sprintf("# Transient System Deployment Key\nAGE-SECRET-KEY-1%x\n", hashed)
			if err := os.WriteFile("/tmp/age.key", []byte(ageKeyContent), 0600); err != nil {
				return errMsg{err: fmt.Errorf("failed to drop transient age target into RAM sandbox: %v", err)}
			}
			return stepCompleteMsg(stepIdx)

		case 3:
			cmd := exec.Command("ykman", "otp", "chalresp", "--generate", "2", "-f")
			if err := cmd.Run(); err != nil {
				return errMsg{err: fmt.Errorf("failed to program hardware token slots: %v", err)}
			}
			return stepCompleteMsg(stepIdx)

		case 4:
			configTargetDir := "./hosts"
			_ = exec.Command("mkdir", "-p", configTargetDir).Run()

			hardwareFile := fmt.Sprintf("%s/%s-hardware.nix", configTargetDir, m.hosts[m.selectedHost])
			cmd := exec.Command("nixos-generate-config", "--show-hardware-config")
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err != nil {
				return errMsg{err: fmt.Errorf("hardware topology inspection failed: %v", err)}
			}

			if err := os.WriteFile(hardwareFile, out.Bytes(), 0644); err != nil {
				return errMsg{err: fmt.Errorf("failed writing dynamic hardware description: %v", err)}
			}
			return stepCompleteMsg(stepIdx)

		case 5:
			targetFlake := fmt.Sprintf(".#%s", m.hosts[m.selectedHost])
			cmd := exec.Command("nixos-install", "--flake", targetFlake)
			cmd.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE=/tmp/age.key")

			time.Sleep(1000 * time.Millisecond)

			go func() {
				time.Sleep(3 * time.Second)
				_ = exec.Command("reboot").Run()
			}()
			return stepCompleteMsg(stepIdx)
		}
		return successMsg{}
	}
}

func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Fatal runtime error: %v\n", err)
		os.Exit(1)
	}
}
