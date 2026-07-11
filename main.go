package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/crypto/argon2"
)

var BuildDate = "version 7 (Multi-EFI / XBOOTLDR)"

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
	stateSelectEFI
	stateInputUsername
	stateInputPassphrase
	stateSelectWiFi
	stateInputWiFiPass
	stateDeploying
	stateFailed
	stateUpdating
)

type installStep struct {
	name string
	log  string
}

type Config struct {
	Hosts []string `json:"hosts"`
}

type model struct {
	state         sessionState
	hosts         []string
	selectedHost  int
	disks         []string
	selectedDisk  int
	targetDisk    string
	efiPartitions []string
	selectedEFI   int
	targetEFIDisk string
	wifis         []string
	selectedWiFi  int
	wifiSSID      string
	wifiPass      string
	steps         []installStep
	currentStep   int
	progress      progress.Model
	textInput     textinput.Model
	username      string
	masterPhrase  string
	yubiSerial    string
	done          bool
	err           error
	logs          []string
	msgChan       chan tea.Msg
	shouldRestart bool
	newBinaryPath string
}

type stepCompleteMsg int
type logMsg string
type successMsg struct{}
type errMsg struct{ err error }
type installStartedMsg struct{ ch chan tea.Msg }
type triggerRestartMsg struct{ binaryPath string }

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
		logs:      []string{fmt.Sprintf("System initialized (Build: %s). Please select target infrastructure profile.", BuildDate)},
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	if m.state == stateFailed {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q", "Q":
				return m, tea.Quit
			case "r", "R":
				m.state = stateUpdating
				return m, selfUpdateAndRestartCmd()
			}
		}
		return m, nil
	}

	if m.state == stateUpdating {
		switch msg := msg.(type) {
		case triggerRestartMsg:
			m.shouldRestart = true
			m.newBinaryPath = msg.binaryPath
			return m, tea.Quit
		case errMsg:
			m.err = msg.err
			m.state = stateFailed
			return m, nil
		}
		return m, nil
	}

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
					m.state = stateFailed
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

				m.efiPartitions = getEFIPartitions()
				m.efiPartitions = append(m.efiPartitions, "Create New EFI Partition (1GB)")
				m.selectedEFI = 0
				m.state = stateSelectEFI

				m.logs = append(m.logs, fmt.Sprintf("💾 Target disk set: %s. Awaiting EFI selection...", m.targetDisk))
				return m, nil
			}
		}

	case stateSelectEFI:
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.selectedEFI > 0 {
					m.selectedEFI--
				}
			case "down", "j":
				if m.selectedEFI < len(m.efiPartitions)-1 {
					m.selectedEFI++
				}
			case "enter":
				selection := m.efiPartitions[m.selectedEFI]
				if strings.Contains(selection, "Create New EFI Partition") {
					m.targetEFIDisk = ""
					m.logs = append(m.logs, "💿 EFI Choice: Will create new EFI partition on target disk.")
				} else {
					parts := strings.Split(selection, " ")
					m.targetEFIDisk = parts[0]
					m.logs = append(m.logs, fmt.Sprintf("💿 EFI Choice: Using existing partition -> %s", m.targetEFIDisk))
				}

				m.state = stateInputUsername
				m.textInput.Reset()
				m.textInput.Placeholder = "Enter target username (e.g. vladimir)..."
				m.textInput.EchoMode = textinput.EchoNormal
				m.textInput.Focus()
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
					m.state = stateFailed
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
					m.state = stateFailed
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

		case installStartedMsg:
			m.msgChan = msg.ch
			return m, checkMsgChannel(m.msgChan)

		case logMsg:
			cleanLine := strings.TrimSpace(string(msg))
			if cleanLine != "" {
				if m.currentStep < len(m.steps) {
					m.steps[m.currentStep].log = cleanLine
				}
				m.logs = append(m.logs, cleanLine)
				if len(m.logs) > 10 {
					m.logs = m.logs[1:]
				}
			}
			if m.msgChan != nil {
				return m, checkMsgChannel(m.msgChan)
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
			m.state = stateFailed
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

	if m.state == stateFailed {
		s.WriteString("\n" + titleStyle.Render(fmt.Sprintf("NixOS Bootstrapper (%s)", BuildDate)) + "\n\n")
		s.WriteString(errorStyle.Render("❌ DEPLOYMENT CRASHED!") + "\n\n")
		s.WriteString(fmt.Sprintf("Reason:\n%v\n\n", errorStyle.Render(m.err.Error())))
		s.WriteString("─────────────────────────────────────────────────────────────────\n")
		s.WriteString("Actions Available:\n")
		s.WriteString(" [ " + focusedStyle.Render("R") + " ] Hot-reload runner using latest GitHub release\n")
		s.WriteString(" [ " + errorStyle.Render("Q") + " ] Terminate deployment process\n")
		s.WriteString("─────────────────────────────────────────────────────────────────\n\n")

		s.WriteString("📋 Last Runtime Logs:\n")
		for _, log := range m.logs {
			s.WriteString(" " + log + "\n")
		}
		return s.String()
	}

	if m.state == stateUpdating {
		s.WriteString("\n" + titleStyle.Render(fmt.Sprintf("NixOS Bootstrapper (%s)", BuildDate)) + "\n\n")
		s.WriteString("⏳ " + focusedStyle.Render("Hot-reloading runtime environment on the fly...") + "\n\n")
		s.WriteString(" • Querying GitHub Releases API (fetching latest production asset)...\n")
		s.WriteString(" • Extracting tar.gz bundle into volatile memory sandbox (/tmp)...\n")
		s.WriteString(" • Swapping current runtime lifecycle via atomic syscall.Exec...\n\n")
		s.WriteString(" Please maintain ecosystem power. This transition will conclude in moments.")
		return s.String()
	}

	s.WriteString("\n" + titleStyle.Render(fmt.Sprintf("NixOS Bootstrapper (%s)", BuildDate)) + "\n\n")

	if m.state == stateSelectHost {
		s.WriteString(" Select Target Host Architecture Profile:\n")
		for i, host := range m.hosts {
			if m.selectedHost == i {
				s.WriteString(focusedStyle.Render(fmt.Sprintf("  ➔  %s\n", host)))
			} else {
				s.WriteString(fmt.Sprintf("      %s\n", host))
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
				s.WriteString(fmt.Sprintf("      %s\n", disk))
			}
		}
		s.WriteString("\n [ Navigation: Up/Down or J/K • Selection: Enter ]\n\n")
	}

	if m.state == stateSelectEFI {
		s.WriteString(" Select EFI Boot Partition:\n")
		for i, efi := range m.efiPartitions {
			if m.selectedEFI == i {
				s.WriteString(focusedStyle.Render(fmt.Sprintf("  ➔  %s\n", efi)))
			} else {
				s.WriteString(fmt.Sprintf("      %s\n", efi))
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
				s.WriteString(fmt.Sprintf("      %s\n", wifi))
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

	if len(m.logs) > 0 {
		s.WriteString("📝 Security Infrastructure Logs:\n")
		s.WriteString("--------------------------------------------------\n")
		for _, log := range m.logs {
			s.WriteString(" " + log + "\n")
		}
		s.WriteString("--------------------------------------------------\n")
	}

	if m.done {
		s.WriteString("\n" + successStyle.Render("✨ Finished! Profile targets successfully committed. Rebooting ecosystem...") + "\n")
	}

	return s.String()
}

func checkMsgChannel(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func selfUpdateAndRestartCmd() tea.Cmd {
	return func() tea.Msg {
		downloadURL := "https://github.com/withoutboat/nixos-bootstrapper/releases/latest/download/nixos-bootstrapper-linux-amd64.tar.gz"
		tarPath := "/tmp/bootstrapper.tar.gz"
		extractedBinPath := "/tmp/nixos-bootstrapper"

		resp, err := http.Get(downloadURL)
		if err != nil {
			return errMsg{err: fmt.Errorf("network fault updating from GitHub: %v", err)}
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return errMsg{err: fmt.Errorf("GitHub validation error (status %d)", resp.StatusCode)}
		}

		out, err := os.Create(tarPath)
		if err != nil {
			return errMsg{err: fmt.Errorf("failed creating RAM storage for binary: %v", err)}
		}

		if _, err = io.Copy(out, resp.Body); err != nil {
			out.Close()
			return errMsg{err: fmt.Errorf("failed pipeline extraction to /tmp: %v", err)}
		}
		out.Close()

		if outLog, err := exec.Command("tar", "-xzf", tarPath, "-C", "/tmp").CombinedOutput(); err != nil {
			return errMsg{err: fmt.Errorf("failed extraction workflow: %v\nLog: %s", err, string(outLog))}
		}
		_ = os.Remove(tarPath)

		if err := os.Chmod(extractedBinPath, 0755); err != nil {
			return errMsg{err: fmt.Errorf("chmod execution denial: %v", err)}
		}

		return triggerRestartMsg{binaryPath: extractedBinPath}
	}
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

			if m.targetEFIDisk != "" {
				exec.Command("mkdir", "-p", "/mnt/efi").Run()
				if out, err := exec.Command("mount", m.targetEFIDisk, "/mnt/efi").CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to mount existing efi: %v\nOutput: %s", err, string(out))}
				}

				if out, err := exec.Command("sgdisk", "-n", "1:0:+1G", "-t", "1:ea00", "-c", "1:boot", disk).CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to create xbootldr partition: %v\nOutput: %s", err, string(out))}
				}
				bootPart := disk + "1"
				if strings.Contains(disk, "nvme") || strings.Contains(disk, "mmcblk") {
					bootPart = disk + "p1"
				}
				if out, err := exec.Command("mkfs.fat", "-F", "32", "-n", "boot", bootPart).CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to format xbootldr (fat32): %v\nOutput: %s", err, string(out))}
				}
				rootPartNum := "2"

				if out, err := exec.Command("sgdisk", "-n", rootPartNum+":0:0", "-t", rootPartNum+":8300", "-c", rootPartNum+":root", disk).CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to create root partition: %v\nOutput: %s", err, string(out))}
				}
				exec.Command("partprobe", disk).Run()
				time.Sleep(2 * time.Second)

				rootPart := disk + rootPartNum
				if strings.Contains(disk, "nvme") || strings.Contains(disk, "mmcblk") {
					rootPart = disk + "p" + rootPartNum
				}

				if out, err := exec.Command("mkfs.ext4", "-F", "-L", "nixos", rootPart).CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to format root (ext4): %v\nOutput: %s", err, string(out))}
				}

				exec.Command("udevadm", "settle").Run()

				if out, err := exec.Command("mount", "/dev/disk/by-label/nixos", "/mnt").CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to mount root: %v\nOutput: %s", err, string(out))}
				}
				exec.Command("mkdir", "-p", "/mnt/boot").Run()
				if out, err := exec.Command("mount", bootPart, "/mnt/boot").CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to mount boot: %v\nOutput: %s", err, string(out))}
				}
			} else {
				if out, err := exec.Command("sgdisk", "-Z", disk).CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to wipe disk: %v\nOutput: %s", err, string(out))}
				}

				if out, err := exec.Command("sgdisk", "-n", "1:0:+1G", "-t", "1:ef00", "-c", "1:boot", disk).CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to create boot partition: %v\nOutput: %s", err, string(out))}
				}
				bootPart := disk + "1"
				if strings.Contains(disk, "nvme") || strings.Contains(disk, "mmcblk") {
					bootPart = disk + "p1"
				}
				if out, err := exec.Command("mkfs.fat", "-F", "32", "-n", "boot", bootPart).CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to format boot (fat32): %v\nOutput: %s", err, string(out))}
				}

				rootPartNum := "2"
				if out, err := exec.Command("sgdisk", "-n", rootPartNum+":0:0", "-t", rootPartNum+":8300", "-c", rootPartNum+":root", disk).CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to create root partition: %v\nOutput: %s", err, string(out))}
				}
				exec.Command("partprobe", disk).Run()
				time.Sleep(2 * time.Second)

				rootPart := disk + rootPartNum
				if strings.Contains(disk, "nvme") || strings.Contains(disk, "mmcblk") {
					rootPart = disk + "p" + rootPartNum
				}

				if out, err := exec.Command("mkfs.ext4", "-F", "-L", "nixos", rootPart).CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to format root (ext4): %v\nOutput: %s", err, string(out))}
				}

				exec.Command("udevadm", "settle").Run()

				if out, err := exec.Command("mount", "/dev/disk/by-label/nixos", "/mnt").CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to mount root: %v\nOutput: %s", err, string(out))}
				}
				exec.Command("mkdir", "-p", "/mnt/boot").Run()
				if out, err := exec.Command("mount", bootPart, "/mnt/boot").CombinedOutput(); err != nil {
					return errMsg{err: fmt.Errorf("failed to mount boot: %v\nOutput: %s", err, string(out))}
				}
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
			configStr := string(out)
			lastBrace := strings.LastIndex(configStr, "}")
			cpuProfile := detectCPU()
			gpuProfile := detectGPU()
			nvidiaOpen := isNvidiaOpenCapable()
			intelID, nvidiaID := getBusIDs()

			bootUUIDCmd := exec.Command("sh", "-c", "blkid -s UUID -o value $(findmnt -n -o SOURCE /mnt/boot)")
			bootUUIDOut, _ := bootUUIDCmd.Output()
			bootUUID := strings.TrimSpace(string(bootUUIDOut))

			var efiUUID string
			if m.targetEFIDisk != "" {
				uuidCmd := exec.Command("blkid", "-s", "UUID", "-o", "value", m.targetEFIDisk)
				uuidOut, _ := uuidCmd.Output()
				efiUUID = strings.TrimSpace(string(uuidOut))
			}

			if lastBrace != -1 && bootUUID != "" {
				var injection string
				if m.targetEFIDisk != "" && efiUUID != "" {
					injection = fmt.Sprintf(`
            fileSystems."/boot" = {
              device = "/dev/disk/by-uuid/%s";
              fsType = "vfat";
              options = [ "defaults" "umask=0077" ];
            };
            fileSystems."/efi" = {
              device = "/dev/disk/by-uuid/%s";
              fsType = "vfat";
              options = [ "defaults" "umask=0077" ];
            };
            boot.loader.efi.efiSysMountPoint = "/efi";
            boot.loader.systemd-boot.enable = true;
            boot.loader.systemd-boot.configurationLimit = 15;
            boot.loader.efi.canTouchEfiVariables = true;

            _module.args.spec = {
              username = "%s";
              cpu = "%s";
              gpu = "%s";
              nvidiaOpen = %t;
            };
            hardware.nvidia.prime = {
              intelBusId = "%s";
              nvidiaBusId = "%s";
            };
          `, bootUUID, efiUUID, m.username, cpuProfile, gpuProfile, nvidiaOpen, intelID, nvidiaID)
				} else {
					injection = fmt.Sprintf(`
            fileSystems."/boot" = {
              device = "/dev/disk/by-uuid/%s";
              fsType = "vfat";
              options = [ "defaults" "umask=0077" ];
            };
            boot.loader.efi.efiSysMountPoint = "/boot";
            boot.loader.systemd-boot.enable = true;
            boot.loader.systemd-boot.configurationLimit = 15;
            boot.loader.efi.canTouchEfiVariables = true;

            _module.args.spec = {
              username = "%s";
              cpu = "%s";
              gpu = "%s";
              nvidiaOpen = %t;
            };
            hardware.nvidia.prime = {
              intelBusId = "%s";
              nvidiaBusId = "%s";
            };
          `, bootUUID, m.username, cpuProfile, gpuProfile, nvidiaOpen, intelID, nvidiaID)
				}
				configStr = configStr[:lastBrace] + injection + configStr[lastBrace:]
			}
			hardwareFile := filepath.Join(targetSysConfigDir, "hardware-configuration.nix")
			if err := os.WriteFile(hardwareFile, []byte(configStr), 0644); err != nil {
				return errMsg{err: fmt.Errorf("failed writing hardware-configuration.nix: %v", err)}
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

			buildDir, err := os.MkdirTemp("/tmp", "nix-build-flake-*")
			if err != nil {
				return errMsg{err: fmt.Errorf("failed to create secure temp build dir: %v", err)}
			}

			if out, err := exec.Command("cp", "-r", nixCoreDir+"/.", buildDir).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to copy nix-core to build dir: %v\nOutput: %s", err, string(out))}
			}

			targetHardwareFile := filepath.Join(buildDir, "hardware.nix")
			if out, err := exec.Command("cp", "/mnt/etc/nixos/hardware-configuration.nix", targetHardwareFile).CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to copy hardware config: %v\nOutput: %s", err, string(out))}
			}

			_ = exec.Command("rm", "-rf", filepath.Join(buildDir, ".git")).Run()
			_ = exec.Command("find", buildDir, "-name", ".gitignore", "-type", "f", "-delete").Run()
			exec.Command("git", "-C", buildDir, "add", "-A").Run()
			exec.Command("git", "-C", buildDir, "init").Run()
			exec.Command("git", "-C", buildDir, "config", "user.name", "bootstrapper").Run()
			exec.Command("git", "-C", buildDir, "config", "user.email", "boot@strapper.org").Run()

			if out, err := exec.Command("git", "-C", buildDir, "add", "-A").CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to git add: %v\nOutput: %s", err, string(out))}
			}

			if _, err := os.Stat(filepath.Join(buildDir, "hardware.nix")); os.IsNotExist(err) {
				m.logs = append(m.logs, "FATAL: hardware.nix does not exist in buildDir!")
			} else {
				m.logs = append(m.logs, "OK: hardware.nix exists in buildDir")
			}

			if out, err := exec.Command("git", "-C", buildDir, "commit", "-m", "stable-deterministic-deploy").CombinedOutput(); err != nil {
				return errMsg{err: fmt.Errorf("failed to git commit: %v\nOutput: %s", err, string(out))}
			}

			_ = exec.Command("sync").Run()

			targetFlake := fmt.Sprintf("git+file://%s#%s", buildDir, m.hosts[m.selectedHost])
			cmd := exec.Command("nixos-install", "--flake", targetFlake, "--no-root-passwd", "--option", "eval-cache", "false", "--option", "tarball-ttl", "0")
			cmd.Env = append(os.Environ(), "SOPS_AGE_KEY_FILE=/tmp/age.key", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=safe.directory", "GIT_CONFIG_VALUE_0=*")

			stdout, err := cmd.StdoutPipe()
			if err != nil {
				return errMsg{err: fmt.Errorf("failed to init log stream pipe: %v", err)}
			}
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				return errMsg{err: fmt.Errorf("failed to start deployment: %v", err)}
			}

			m.logs = append(m.logs, "🔐 Enrolling YubiKey to LUKS slot (FIDO2)...")
			enrollCmd := "systemd-cryptenroll --fido2-device=auto --fido2-with-user-presence=yes /dev/disk/by-partlabel/disk-main-luks"
			if _, err := exec.Command("sh", "-c", enrollCmd).CombinedOutput(); err != nil {
				m.logs = append(m.logs, fmt.Sprintf("⚠️ LUKS YubiKey Error (ignored): %v", err))
			} else {
				m.logs = append(m.logs, "✅ YubiKey enrolled to LUKS successfully.")
			}

			msgChan := make(chan tea.Msg)
			go func(ch chan tea.Msg, homeDir string, bDir string) {
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					ch <- logMsg(scanner.Text())
				}

				if err := cmd.Wait(); err != nil {
					ch <- errMsg{err: fmt.Errorf("nixos-install deployment broke: %v", err)}
					return
				}

				m.logs = append(m.logs, "⏳ Awaiting YubiKey touch to generate U2F mapping...")
				mappingCmd := fmt.Sprintf("pamu2fcfg -u %s > /mnt/etc/u2f_mappings", m.username)
				if out, err := exec.Command("sh", "-c", mappingCmd).CombinedOutput(); err != nil {
					ch <- errMsg{err: fmt.Errorf("failed to generate u2f mapping: %v\nOutput: %s", err, string(out))}
				}
				m.logs = append(m.logs, "✅ U2F mapping generated successfully.")

				_ = exec.Command("chown", "-R", "1000:100", homeDir).Run()
				_ = exec.Command("rm", "-rf", bDir).Run()

				ch <- stepCompleteMsg(7)
			}(msgChan, userHomeDir, buildDir)

			return installStartedMsg{ch: msgChan}
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

func getEFIPartitions() []string {
	out, err := exec.Command("sh", "-c", "blkid -t TYPE=vfat -o device").CombinedOutput()
	if err != nil {
		return []string{}
	}

	var partitions []string
	devices := strings.Split(strings.TrimSpace(string(out)), "\n")

	for _, dev := range devices {
		if dev == "" {
			continue
		}
		lsblkOut, err := exec.Command("lsblk", "-n", "-p", "-o", "NAME,SIZE,PKNAME,DISK-SIZE", dev).CombinedOutput()
		if err != nil {
			continue
		}
		parts := strings.Fields(strings.TrimSpace(string(lsblkOut)))
		if len(parts) >= 2 {
			name := parts[0]
			size := parts[1]
			parent := "unknown"
			if len(parts) >= 3 {
				parent = parts[2]
			}
			diskSize := "unknown"
			if len(parts) >= 4 {
				diskSize = parts[3]
			}

			entry := fmt.Sprintf("%s (%s) | Disk: %s [%s]", name, size, parent, diskSize)
			partitions = append(partitions, entry)
		}
	}
	return partitions
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

func main() {
	p := tea.NewProgram(initialModel())
	resModel, err := p.Run()
	if err != nil {
		fmt.Printf("Fatal runtime error: %v\n", err)
		os.Exit(1)
	}
	if m, ok := resModel.(model); ok && m.shouldRestart {
		fmt.Println("\n[INFO] Handing over terminal process stack to the new binary release...")
		err = syscall.Exec(m.newBinaryPath, os.Args, os.Environ())
		if err != nil {
			fmt.Printf("[ERROR] Process hot-swap deployment crashed: %v\n", err)
			os.Exit(1)
		}
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
	hasVGA, hasNvidia, hasAMD, hasIntel := false, false, false, false
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
	return strings.Contains(content, "rtx 20") || strings.Contains(content, "rtx 30") || strings.Contains(content, "rtx 40") || strings.Contains(content, "rtx 50")
}

func getBusIDs() (string, string) {
	out, _ := exec.Command("lspci", "-nn").CombinedOutput()
	lines := strings.Split(string(out), "\n")

	var intelID, nvidiaID string
	for _, line := range lines {
		l := strings.ToLower(line)
		if strings.Contains(l, "vga") || strings.Contains(l, "3d") {
			parts := strings.Split(line, " ")
			bus := parts[0]

			segments := strings.Split(bus, ":")
			if len(segments) < 2 {
				continue
			}

			devAndFunc := strings.Split(segments[1], ".")

			busNum := fmt.Sprintf("%d", parseInt(segments[0]))
			devNum := fmt.Sprintf("%d", parseInt(devAndFunc[0]))

			formatted := fmt.Sprintf("PCI:%s:%s:0", busNum, devNum)

			if strings.Contains(l, "nvidia") {
				nvidiaID = formatted
			} else if strings.Contains(l, "intel") {
				intelID = formatted
			}
		}
	}
	return intelID, nvidiaID
}

func parseInt(s string) int64 {
	val, _ := strconv.ParseInt(s, 16, 64)
	return val
}
