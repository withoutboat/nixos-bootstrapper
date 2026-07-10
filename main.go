package main

import (
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
	stateSelectDisk
	stateInputUsername
	stateInputPassphrase
	stateSelectWiFi
	stateInputWiFiPass
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
	disks        []string
	selectedDisk int
	targetDisk   string
	wifis        []string
	selectedWiFi int
	wifiSSID     string
	wifiPass     string
	steps        []installStep
	currentStep  int
	progress     progress.Model
	textInput    textinput.Model
	username     string
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
	ti.Placeholder = "Enter target username (e.g. vladimir)..."
	ti.Focus()

	availableHosts := []string{"pc-th"}

	exePath, err := os.Executable()
	if err == nil {
		realPath, err := filepath.EvalSymlinks(exePath)
		if err == nil {
			exePath = realPath
		}
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
			{name: "Partition & Mount Target Disk", log: "Pending..."},
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
				m.disks = getAvailableDisks()
				if len(m.disks) == 0 {
					m.err = fmt.Errorf("no suitable disks found for installation")
					return m, nil
				}
				m.state = stateSelectDisk
				m.logs = append(m.logs, fmt.Sprintf("🎯 Profile target set: %s. Awaiting disk selection...", m.hosts[m.selectedHost]))
				return m, nil
			}
		}

	case stateSelectDisk:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.selectedDisk > 0 {
					m.selectedDisk--
				}
			case "down", "j":
				if m.selectedDisk < len(m.disks)-1 {
					m.selectedDisk++
				}
			case "enter":
				m.targetDisk = strings.Split(m.disks[m.selectedDisk], " ")[0]
				m.state = stateInputUsername
				m.textInput.Reset()
				m.textInput.Placeholder = "Enter target username (e.g. vladimir)..."
				m.textInput.EchoMode = textinput.EchoNormal
				m.textInput.Focus()
				m.logs = append(m.logs, fmt.Sprintf("💾 Target disk set: %s. Awaiting username creation...", m.targetDisk))
				return m, nil
			}
		}

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
				m.textInput.EchoMode = textinput.EchoNormal
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
				m.wifis = getWiFiNetworks()
				if len(m.wifis) == 0 {
					m.wifis = []string{"Manual Entry"}
				}
				m.state = stateSelectWiFi
				m.logs = append(m.logs, "🚀 Crypto signature confirmed. Scanning Wi-Fi networks...")
				return m, nil
			}
		}
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd

	case stateSelectWiFi:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.selectedWiFi > 0 {
					m.selectedWiFi--
				}
			case "down", "j":
				if m.selectedWiFi < len(m.wifis)-1 {
					m.selectedWiFi++
				}
			case "enter":
				m.wifiSSID = m.wifis[m.selectedWiFi]
				m.state = stateInputWiFiPass
				m.textInput.Reset()
				if m.wifiSSID == "Manual Entry" {
					m.textInput.Placeholder = "Type SSID manually..."
					m.textInput.EchoMode = textinput.EchoNormal
				} else {
					m.textInput.Placeholder = fmt.Sprintf("Enter Wi-Fi Password for '%s'...", m.wifiSSID)
					m.textInput.EchoMode = textinput.EchoNormal
				}
				m.textInput.Focus()
				return m, nil
			}
		}

	case stateInputWiFiPass:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "enter":
				if m.wifiSSID == "Manual Entry" {
					m.wifiSSID = strings.TrimSpace(m.textInput.Value())
					m.textInput.Reset()
					m.textInput.Placeholder = fmt.Sprintf("Enter Wi-Fi Password for '%s'...", m.wifiSSID)
					m.textInput.EchoMode = textinput.EchoNormal
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

	if m.state == stateSelectDisk {
		s.WriteString(errorStyle.Render(" WARNING: SELECTED DISK WILL BE COMPLETELY WIPED!\n"))
		s.WriteString(" Select Target Installation Disk:\n")
		for i, disk := range m.disks {
			if m.selectedDisk == i {
				s.WriteString(focusedStyle.Render(fmt.Sprintf("  ➔  %s\n", disk)))
			} else {
				s.WriteString(fmt.Sprintf("     %s\n", disk))
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

	if m.state == stateSelectWiFi {
		s.WriteString("📶 Select Wi-Fi Network:\n")
		for i, wifi := range m.wifis {
			if m.selectedWiFi == i {
				s.WriteString(focusedStyle.Render(fmt.Sprintf("  ➔  %s\n", wifi)))
			} else {
				s.WriteString(fmt.Sprintf("     %s\n", wifi))
			}
		}
		s.WriteString("\n [ Navigation: Up/Down or J/K • Selection: Enter ]\n\n")
	}

	if m.state == stateInputWiFiPass {
		s.WriteString(fmt.Sprintf("🔑 ENTER PASSWORD FOR '%s':\n%s\n\n", m.wifiSSID, m.textInput.View()))
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
		s.WriteString("\n" + errorStyle.Render(fmt.Sprintf("Deployment failure:\n%v", m.err)) + "\n")
	} else if m.done {
		s.WriteString("\n" + successStyle.Render("✨ Finished! Profile targets successfully committed. Rebooting ecosystem...") + "\n")
	}

	return s.String()
}

func (m *model) runStep(stepIdx int) tea.Cmd {
	return func() tea.Msg {
		switch stepIdx {
		case 0:
			out, err := exec.Command("ykman", "--version").CombinedOutput()
			if err != nil {
				return errMsg{err: fmt.Errorf("yubikey manager (ykman) missing: %v\nOutput: %s", err, string(out))}
			}
			time.Sleep(300 * time.Millisecond)
			return stepCompleteMsg(stepIdx)

		case 1:
			disk := m.targetDisk

			exec.Command("umount", "-R", "/mnt").Run()
			exec.Command("swapoff", "-a").Run()

			if out, err := exec.Command("sgdisk", "-Z", disk).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to wipe disk: %v\nOutput: %s", err, string(out))}
			}
			if out, err := exec.Command("sgdisk", "-n", "1:0:+512M", "-t", "1:ef00", "-c", "1:boot", disk).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to create boot partition: %v\nOutput: %s", err, string(out))}
			}
			if out, err := exec.Command("sgdisk", "-n", "2:0:0", "-t", "2:8300", "-c", "2:root", disk).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to create root partition: %v\nOutput: %s", err, string(out))}
			}

			exec.Command("partprobe", disk).Run()
			time.Sleep(2 * time.Second)

			part1 := disk + "1"
			part2 := disk + "2"
			if strings.Contains(disk, "nvme") || strings.Contains(disk, "mmcblk") {
				part1 = disk + "p1"
				part2 = disk + "p2"
			}

			if out, err := exec.Command("mkfs.fat", "-F", "32", "-n", "boot", part1).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to format boot (fat32): %v\nOutput: %s", err, string(out))}
			}
			if out, err := exec.Command("mkfs.ext4", "-F", "-L", "nixos", part2).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to format root (ext4): %v\nOutput: %s", err, string(out))}
			}
			exec.Command("udevadm", "settle").Run()

			if out, err := exec.Command("mount", "/dev/disk/by-label/nixos", "/mnt").CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to mount root: %v\nOutput: %s", err, string(out))}
			}
			exec.Command("mkdir", "-p", "/mnt/boot").Run()
			if out, err := exec.Command("mount", "/dev/disk/by-label/boot", "/mnt/boot").CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to mount boot: %v\nOutput: %s", err, string(out))}
			}

			return stepCompleteMsg(stepIdx)

		case 2:
			exec.Command("nmcli", "radio", "wifi", "on").Run()

			if m.wifiSSID != "Manual Entry" && m.wifiSSID != "" {
				out, err := exec.Command("nmcli", "dev", "wifi", "connect", m.wifiSSID, "password", m.wifiPass).CombinedOutput()
				if err != nil {
					return errMsg{err: fmt.Errorf("wifi connection failed: %v\nOutput: %s", err, string(out))}
				}
			}
			return stepCompleteMsg(stepIdx)

		case 3:
			cmd := exec.Command("ykman", "list")
			out, err := cmd.CombinedOutput()
			if err != nil {
				return errMsg{err: fmt.Errorf("hardware token authentication missing: %v\nOutput: %s", err, string(out))}
			}

			fields := strings.Fields(string(out))
			for i, field := range fields {
				if field == "Serial:" && i+1 < len(fields) {
					m.yubiSerial = fields[i+1]
				}
			}
			if m.yubiSerial == "" {
				m.yubiSerial = "fallback-hardware-token-salt-2026"
			}
			return stepCompleteMsg(stepIdx)

		case 4:
			salt := []byte("yubikey-salt-" + m.yubiSerial)
			rawKey := argon2.IDKey([]byte(m.masterPhrase), salt, 3, 64*1024, 4, 64)
			hashed := sha256.Sum256(rawKey)

			ageKeyContent := fmt.Sprintf("# Transient System Deployment Key\nAGE-SECRET-KEY-1%x\n", hashed)
			if err := os.WriteFile("/tmp/age.key", []byte(ageKeyContent), 0600); err != nil {
				return errMsg{err: fmt.Errorf("failed to drop transient age target into RAM sandbox: %v", err)}
			}
			return stepCompleteMsg(stepIdx)

		case 5:
			out, err := exec.Command("ykman", "otp", "chalresp", "--generate", "2", "-f").CombinedOutput()
			if err != nil {
				return errMsg{err: fmt.Errorf("failed to program hardware token slots: %v\nOutput: %s", err, string(out))}
			}
			return stepCompleteMsg(stepIdx)

		case 6:
			targetSysConfigDir := "/mnt/etc/nixos"
			_ = exec.Command("mkdir", "-p", targetSysConfigDir).Run()

			out, err := exec.Command("nixos-generate-config", "--root", "/mnt", "--show-hardware-config").CombinedOutput()
			if err != nil {
				return errMsg{err: fmt.Errorf("hardware topology inspection failed: %v\nOutput: %s", err, string(out))}
			}

			hardwareFile := filepath.Join(targetSysConfigDir, "hardware-configuration.nix")
			if err := os.WriteFile(hardwareFile, out, 0644); err != nil {
				return errMsg{err: fmt.Errorf("failed writing dynamic hardware description: %v", err)}
			}
			return stepCompleteMsg(stepIdx)

		case 7:
			userHomeDir := fmt.Sprintf("/mnt/home/%s", m.username)
			nixCoreDir := filepath.Join(userHomeDir, "nix-core")
			nixHomeDir := filepath.Join(userHomeDir, "nix-home")

			_ = exec.Command("mkdir", "-p", userHomeDir).Run()

			if out, err := exec.Command("git", "clone", "https://github.com/withoutboat/nix-core.git", nixCoreDir).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to clone nix-core: %v\nOutput: %s", err, string(out))}
			}

			if out, err := exec.Command("git", "clone", "https://github.com/withoutboat/nix-home.git", nixHomeDir).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to clone nix-home: %v\nOutput: %s", err, string(out))}
			}

			cpuProfile := detectCPU()
			gpuProfile := detectGPU()
			nvidiaOpen := isNvidiaOpenCapable()

			userContextFile := filepath.Join(nixCoreDir, "hosts", "runtime-context.nix")
			userContextContent := fmt.Sprintf("{\n  username = \"%s\";\n  cpu = \"%s\";\n  gpu = \"%s\";\n  nvidiaOpen = %t;\n}\n", m.username, cpuProfile, gpuProfile, nvidiaOpen)
			if err := os.WriteFile(userContextFile, []byte(userContextContent), 0644); err != nil {
				return errMsg{err: fmt.Errorf("failed to write runtime context data: %v", err)}
			}

			targetModulesDir := filepath.Join(nixCoreDir, "hosts", "modules")
			if err := os.MkdirAll(targetModulesDir, 0755); err != nil {
				return errMsg{err: fmt.Errorf("failed to create target modules directory: %v", err)}
			}

			if out, err := exec.Command("cp", "/mnt/etc/nixos/hardware-configuration.nix", filepath.Join(targetModulesDir, "hardware.nix")).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to copy hardware config into flake repo: %v\nOutput: %s", err, string(out))}
			}

			targetFlake := fmt.Sprintf("path:%s#%s", nixCoreDir, m.hosts[m.selectedHost])

			cmd := exec.Command("nixos-install", "--flake", targetFlake, "--no-root-passwd")
			cmd.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE=/tmp/age.key")

			out, err := cmd.CombinedOutput()
			if err != nil {
				return errMsg{err: fmt.Errorf("nixos-install execution failed: %v\nOutput snippet: %s", err, truncateString(string(out), 800))}
			}

			_ = exec.Command("chown", "-R", "1000:100", userHomeDir).Run()

			go func() {
				time.Sleep(3 * time.Second)
				_ = exec.Command("reboot").Run()
			}()
			return stepCompleteMsg(stepIdx)
		}
		return successMsg{}
	}
}

func getAvailableDisks() []string {
	out, err := exec.Command("lsblk", "-d", "-n", "-p", "-o", "NAME,SIZE,MODEL").CombinedOutput()
	if err != nil {
		return []string{}
	}

	var disks []string
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if !strings.Contains(line, "loop") && strings.TrimSpace(line) != "" {
			disks = append(disks, strings.TrimSpace(line))
		}
	}
	return disks
}

func getWiFiNetworks() []string {
	out, err := exec.Command("nmcli", "-t", "-f", "SSID", "dev", "wifi", "list").CombinedOutput()
	if err != nil {
		return []string{}
	}

	ssidMap := make(map[string]bool)
	var ssids []string
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		ssid := strings.TrimSpace(line)
		if ssid != "" && !ssidMap[ssid] && ssid != "--" {
			ssidMap[ssid] = true
			ssids = append(ssids, ssid)
		}
	}
	ssids = append(ssids, "Manual Entry")
	return ssids
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[len(s)-maxLen:]
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
	out, err := exec.Command("lspci").CombinedOutput()
	if err != nil {
		return "none"
	}

	lines := strings.Split(strings.ToLower(string(out)), "\n")
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
	out, _ := exec.Command("lspci", "-nnk").CombinedOutput()
	content := strings.ToLower(string(out))
	return strings.Contains(content, "rtx 20") ||
		strings.Contains(content, "rtx 30") ||
		strings.Contains(content, "rtx 40") ||
		strings.Contains(content, "rtx 50")
}
