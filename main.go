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
	stateInputUsername
	stateInputPassphrase
	stateInputWiFi
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
	username     string
	masterPhrase string
	wifiSSID     string
	wifiPass     string
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
	ti.Placeholder = "Enter target username (e.g. vladimir)..."
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
			{name: "Connect to Wi-Fi Network", log: "Pending..."},
			{name: "Extract YubiKey metadata for dynamic salt", log: "Pending..."},
			{name: "Generate deterministic transient age key (Argon2id)", log: "Pending..."},
			{name: "Provision YubiKey Slot 2 (Challenge-Response)", log: "Pending..."},
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
				m.state = stateInputUsername
				m.textInput.Reset()
				m.textInput.Placeholder = "Enter target username (e.g. vladimir)..."
				m.textInput.EchoMode = textinput.EchoNormal
				m.textInput.Focus()
				m.logs = append(m.logs, fmt.Sprintf("🎯 Profile target set: %s. Awaiting username creation...", m.hosts[m.selectedHost]))
				return m, nil
			}
		}
		return m, nil

	case stateInputUsername:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "enter":
				m.username = strings.TrimSpace(m.textInput.Value())
				if m.username == "" {
					m.err = fmt.Errorf("username cannot be empty")
					return m, nil
				}
				m.state = stateInputPassphrase
				m.textInput.Reset()
				m.textInput.Placeholder = "Enter your secure master passphrase..."
				m.textInput.EchoMode = textinput.EchoPassword
				m.textInput.Focus()
				m.logs = append(m.logs, fmt.Sprintf("👤 Username registered: %s. Awaiting crypto authority validation...", m.username))
				return m, nil
			}
		}
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd

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
				m.state = stateInputWiFi
				m.textInput.Reset()
				m.textInput.Placeholder = "Enter Wi-Fi SSID..."
				m.textInput.EchoMode = textinput.EchoNormal
				m.textInput.Focus()
				m.logs = append(m.logs, "🚀 Crypto signature confirmed. Awaiting network configuration...")
				return m, nil
			}
		}
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd

	case stateInputWiFi:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "enter":
				if m.wifiSSID == "" {
					m.wifiSSID = strings.TrimSpace(m.textInput.Value())
					if m.wifiSSID == "" {
						m.err = fmt.Errorf("SSID cannot be empty")
						return m, nil
					}
					m.textInput.Reset()
					m.textInput.Placeholder = "Enter Wi-Fi Password..."
					m.textInput.EchoMode = textinput.EchoPassword
					return m, nil
				} else {
					m.wifiPass = m.textInput.Value()
					m.state = stateDeploying
					m.logs = append(m.logs, "🌐 Network details registered. Initiating deployment sequence...")
					return m, m.runStep(0)
				}
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

	if m.state == stateInputUsername {
		s.WriteString(fmt.Sprintf("👤 TARGET USERNAME: %s\n\n", m.textInput.View()))
	}

	if m.state == stateInputPassphrase {
		s.WriteString(fmt.Sprintf("🔑 MASTER PASSPHRASE: %s\n\n", m.textInput.View()))
	}

	if m.state == stateInputWiFi {
		if m.wifiSSID == "" {
			s.WriteString(fmt.Sprintf("📶 ENTER WI-FI SSID:\n%s\n\n", m.textInput.View()))
		} else {
			s.WriteString(fmt.Sprintf("🔑 ENTER PASSWORD FOR '%s':\n%s\n\n", m.wifiSSID, m.textInput.View()))
		}
	}

	if m.state == stateDeploying || m.done {
		for i, step := range m.steps {
			if i < m.currentStep {
				s.WriteString(fmt.Sprintf("  [✓] %s\n", step.name))
			} else if i == m.currentStep && !m.done {
				s.WriteString(fmt.Sprintf("  [➔] %s — %s\n", step.name, statusStyle.Render(step.log)))
			} else {
				s.WriteString(fmt.Sprintf("  [ ] %s\n", step.name))
			}
		}
		s.WriteString("\n " + m.progress.View() + "\n\n")
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
			if err := exec.Command("nmcli", "radio", "wifi", "on").Run(); err != nil {
				return errMsg{err: fmt.Errorf("wifi radio error: %v", err)}
			}
			cmd := exec.Command("nmcli", "dev", "wifi", "connect", m.wifiSSID, "password", m.wifiPass)
			if err := cmd.Run(); err != nil {
				return errMsg{err: fmt.Errorf("wifi connection failed: %v", err)}
			}
			return stepCompleteMsg(stepIdx)

		case 2:
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

		case 3:
			salt := []byte("yubikey-salt-" + m.yubiSerial)
			rawKey := argon2.IDKey([]byte(m.masterPhrase), salt, 3, 64*1024, 4, 64)
			hashed := sha256.Sum256(rawKey)

			ageKeyContent := fmt.Sprintf("# Transient System Deployment Key\nAGE-SECRET-KEY-1%x\n", hashed)
			if err := os.WriteFile("/tmp/age.key", []byte(ageKeyContent), 0600); err != nil {
				return errMsg{err: fmt.Errorf("failed to drop transient age target into RAM sandbox: %v", err)}
			}
			return stepCompleteMsg(stepIdx)

		case 4:
			cmd := exec.Command("ykman", "otp", "chalresp", "--generate", "2", "-f")
			if err := cmd.Run(); err != nil {
				return errMsg{err: fmt.Errorf("failed to program hardware token slots: %v", err)}
			}
			return stepCompleteMsg(stepIdx)

		case 5:
			targetSysConfigDir := "/mnt/etc/nixos"
			_ = exec.Command("mkdir", "-p", targetSysConfigDir).Run()

			cmd := exec.Command("nixos-generate-config", "--root", "/mnt", "--show-hardware-config")
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err != nil {
				return errMsg{err: fmt.Errorf("hardware topology inspection failed: %v", err)}
			}

			hardwareFile := filepath.Join(targetSysConfigDir, "hardware-configuration.nix")
			if err := os.WriteFile(hardwareFile, out.Bytes(), 0644); err != nil {
				return errMsg{err: fmt.Errorf("failed writing dynamic hardware description: %v", err)}
			}
			return stepCompleteMsg(stepIdx)

		case 6:
			userHomeDir := fmt.Sprintf("/mnt/home/%s", m.username)
			nixCoreDir := filepath.Join(userHomeDir, "nix-core")
			nixHomeDir := filepath.Join(userHomeDir, "nix-home")

			_ = exec.Command("mkdir", "-p", userHomeDir).Run()

			repoCoreURL := "https://github.com/withoutboat/nix-core.git"
			cloneCoreCmd := exec.Command("git", "clone", repoCoreURL, nixCoreDir)
			if err := cloneCoreCmd.Run(); err != nil {
				return errMsg{err: fmt.Errorf("failed to clone nix-core ecosystem: %v", err)}
			}

			repoHomeURL := "https://github.com/withoutboat/nix-home.git"
			cloneHomeCmd := exec.Command("git", "clone", repoHomeURL, nixHomeDir)
			if err := cloneHomeCmd.Run(); err != nil {
				return errMsg{err: fmt.Errorf("failed to clone nix-home configuration: %v", err)}
			}

			cpuProfile := detectCPU()
			gpuProfile := detectGPU()
			nvidiaOpen := isNvidiaOpenCapable()

			userContextFile := filepath.Join(nixCoreDir, "hosts", "runtime-context.nix")
			userContextContent := fmt.Sprintf("{\n  username = \"%s\";\n  cpu = \"%s\";\n  gpu = \"%s\";\n  nvidiaOpen = %t;\n}\n", m.username, cpuProfile, gpuProfile, nvidiaOpen)
			if err := os.WriteFile(userContextFile, []byte(userContextContent), 0644); err != nil {
				return errMsg{err: fmt.Errorf("failed to write runtime context data: %v", err)}
			}

			_ = exec.Command("chown", "-R", "1000:100", userHomeDir).Run()

			targetFlake := fmt.Sprintf("%s#%s", nixCoreDir, m.hosts[m.selectedHost])
			cmd := exec.Command("nixos-install", "--flake", targetFlake)
			cmd.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE=/tmp/age.key")

			if err := cmd.Run(); err != nil {
				return errMsg{err: fmt.Errorf("nixos-install execution failed: %v", err)}
			}

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

func detectCPU() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "amd"
	}
	content := strings.ToLower(string(data))
	if strings.Contains(content, "intel") {
		return "intel"
	}
	return "amd"
}

func detectGPU() string {
	cmd := exec.Command("lspci")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "none"
	}

	lines := strings.Split(strings.ToLower(out.String()), "\n")
	hasVGA := false
	hasNvidia := false
	hasAMD := false
	hasIntel := false

	for _, line := range lines {
		if strings.Contains(line, "vga compatible") || strings.Contains(line, "3d controller") {
			hasVGA = true
			if strings.Contains(line, "nvidia") {
				hasNvidia = true
			}
			if strings.Contains(line, "amd") || strings.Contains(line, "ati") {
				hasAMD = true
			}
			if strings.Contains(line, "intel") {
				hasIntel = true
			}
		}
	}

	if !hasVGA {
		return "none"
	}
	if hasNvidia && hasAMD {
		return "hybrid-amd-nvidia"
	}
	if hasNvidia && hasIntel {
		return "intel-nvidia"
	}
	if hasNvidia {
		return "nvidia"
	}
	if hasAMD {
		return "amd"
	}
	if hasIntel {
		return "intel"
	}
	return "none"
}

func isNvidiaOpenCapable() bool {
	cmd := exec.Command("lspci", "-nnk")
	var out bytes.Buffer
	cmd.Stdout = &out
	_ = cmd.Run()

	content := strings.ToLower(out.String())
	return strings.Contains(content, "rtx 20") ||
		strings.Contains(content, "rtx 30") ||
		strings.Contains(content, "rtx 40") ||
		strings.Contains(content, "rtx 50")
}
