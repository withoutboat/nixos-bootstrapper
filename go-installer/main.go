package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// -------------------------------------------------------------------------
// UI Styling (Lipgloss)
// -------------------------------------------------------------------------
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00F5D4")).
			Bold(true).
			MarginLeft(2)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF"))

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00F5D4")).
			Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF007F")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6272A4")).
			Italic(true)
)

// -------------------------------------------------------------------------
// Data Structures for lsblk JSON Parsing
// -------------------------------------------------------------------------
type BlockDevicePart struct {
	Name   string `json:"name"`
	FSType string `json:"fstype"`
	Type   string `json:"type"`
}

type BlockDevice struct {
	Name     string            `json:"name"`
	FSType   string            `json:"fstype"`
	Type     string            `json:"type"`
	Children []BlockDevicePart `json:"children"`
}

type StoragePartitionsRoot struct {
	Devices []BlockDevice `json:"blockdevices"`
}

// -------------------------------------------------------------------------
// Bubble Tea Model Definitions
// -------------------------------------------------------------------------
type installStep struct {
	name string
	log  string
}

type model struct {
	steps       []installStep
	currentStep int
	progress    progress.Model
	done        bool
	err         error
	logs        []string
}

// Bubble Tea State Update Messages
type stepCompleteMsg int
type logMsg string
type successMsg struct{}
type errMsg struct{ err error }

func initialModel() model {
	return model{
		steps: []installStep{
			{name: "Verify Hardware & Storage Geometry", log: "Initializing hardware diagnostics..."},
			{name: "Discover Flashdrive B & Extract Key", log: "Scanning USB device interfaces..."},
			{name: "Partition Target Disk & Create LUKS", log: "Formatting secure storage volumes..."},
			{name: "Mount Filesystems & Prepare ZFS Tree", log: "Configuring ZFS storage pools..."},
			{name: "Clone NixOS Configuration Repositories", log: "Downloading dotfiles configuration..."},
			{name: "Execute nixos-install (Build Environment)", log: "Compiling environment configuration..."},
		},
		progress: progress.New(progress.WithDefaultGradient()),
		logs:     []string{"Press ENTER to begin the automated deployment pipeline..."},
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

// -------------------------------------------------------------------------
// Bubble Tea State Transitions (Update)
// -------------------------------------------------------------------------
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			if !m.done && m.currentStep == 0 && len(m.logs) == 1 {
				m.logs = append(m.logs, "🚀 Launching deployment pipeline...")
				return m, runDeploymentPipeline
			}
		}

	case logMsg:
		m.logs = append(m.logs, string(msg))
		if len(m.logs) > 8 {
			m.logs = m.logs[1:] // Keep log view compact
		}
		return m, nil

	case stepCompleteMsg:
		m.currentStep = int(msg)
		pct := float64(m.currentStep) / float64(len(m.steps))
		cmd := m.progress.SetPercent(pct)
		return m, cmd

	case successMsg:
		m.done = true
		m.progress.SetPercent(1.0)
		m.logs = append(m.logs, "✨ Installation completed successfully! System is ready to reboot.")
		return m, nil

	case errMsg:
		m.err = msg.err
		m.done = true
		m.logs = append(m.logs, fmt.Sprintf("❌ Error: %v", msg.err))
		return m, nil

	case progress.FrameMsg:
		progressModel, cmd := m.progress.Update(msg)
		m.progress = progressModel.(progress.Model)
		return m, cmd
	}

	return m, nil
}

// -------------------------------------------------------------------------
// Bubble Tea Rendering Engine (View)
// -------------------------------------------------------------------------
func (m model) View() string {
	var s strings.Builder

	s.WriteString("\n" + titleStyle.Render("NixOS Automated Secure Bootstrapper") + "\n\n")

	// Print workflow deployment steps
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

	// Real-time runtime log console block
	s.WriteString("📝 System Logs:\n")
	s.WriteString("--------------------------------------------------\n")
	for _, log := range m.logs {
		s.WriteString(" " + log + "\n")
	}
	s.WriteString("--------------------------------------------------\n")

	if m.err != nil {
		s.WriteString("\n" + errorStyle.Render(fmt.Sprintf("Deployment failure: %v", m.err)) + "\n")
	} else if m.done {
		s.WriteString("\n" + successStyle.Render("All steps executed. You can now safely remove Flashdrive B.") + "\n")
	} else {
		s.WriteString("\n" + helpStyle.Render("Controls: Enter — Start Pipeline | Q/Ctrl+C — Exit") + "\n")
	}

	return s.String()
}

// -------------------------------------------------------------------------
// Hardware Logic: Hardware Partition Traversal & Key Extraction
// -------------------------------------------------------------------------
func locateBootstrapAssets() (string, bool) {
	tempMountPoint := "/tmp/key-flashdrive-mnt"
	resolvedKeyPath := "/tmp/active-bootstrap.key"

	// Allocate a temporary runtime workspace inside the Live-CD RAM root
	_ = os.MkdirAll(tempMountPoint, 0755)

	// Fetch hardware block device geometry mapping in raw JSON from lsblk
	cmd := exec.Command("lsblk", "-o", "NAME,FSTYPE,TYPE", "-j")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", false
	}

	var root StoragePartitionsRoot
	if err := json.Unmarshal(out.Bytes(), &root); err != nil {
		return "", false
	}

	// Traverse through active block device mappings and nested partitions
	for _, dev := range root.Devices {
		for _, part := range dev.Children {
			// Isolate dedicated block storage target partitions
			if part.Type != "part" || part.FSType == "" {
				continue
			}

			devNode := "/dev/" + part.Name

			// Execute a transient, read-only system mount to search for credentials
			mountCmd := exec.Command("mount", "-o", "ro", devNode, tempMountPoint)
			if err := mountCmd.Run(); err != nil {
				continue // Mount failed (busy or restricted node), proceed to next device
			}

			targetKeyLocation := filepath.Join(tempMountPoint, "bootstrap.key")

			// Check if the master encryption credential asset lives on this partition
			if _, err := os.Stat(targetKeyLocation); err == nil {
				// Asset resolved! Cache the payload bytes directly into local RAM
				keyData, readErr := os.ReadFile(targetKeyLocation)
				_ = exec.Command("umount", tempMountPoint).Run() // Immediately detach storage asset

				if readErr == nil {
					// Persist key inside RAM disk space with restrictive 0600 file permissions
					_ = os.WriteFile(resolvedKeyPath, keyData, 0600)

					// Inspect for companion home/work declarative configuration repository
					privateEnvSource := filepath.Join(tempMountPoint, "nix-home-work")
					_, privateEnvExists := os.Stat(privateEnvSource)

					return resolvedKeyPath, privateEnvExists == nil
				}
			}

			// Unmount device workspace partition if security keys were not discovered
			_ = exec.Command("umount", tempMountPoint).Run()
		}
	}

	return "", false
}

// -------------------------------------------------------------------------
// Deployment Operations Pipeline
// -------------------------------------------------------------------------
func runDeploymentPipeline() tea.Msg {
	time.Sleep(1 * time.Second)

	// --- Step 1: Geometry Verification (Placeholder action slot) ---
	time.Sleep(1 * time.Second)

	// --- Step 2: Flashdrive B Automation Core ---
	bootstrapKeyFile, hasPrivateEnv := locateBootstrapAssets()
	if bootstrapKeyFile == "" {
		return errMsg{err: fmt.Errorf("security verification failed: Flashdrive B containing 'bootstrap.key' was not found")}
	}

	// --- Step 3: Target Block Cryptsetup Mapping ---
	// Real-world execution hook utilizes `bootstrapKeyFile` string reference:
	// Example: cryptsetup luksFormat /dev/nvme0n1p2 --key-file=bootstrapKeyFile
	_ = bootstrapKeyFile
	_ = hasPrivateEnv

	// Simulate deep OS infrastructure deployment steps
	for i := 1; i <= 6; i++ {
		time.Sleep(1200 * time.Millisecond)
	}

	return successMsg{}
}

// -------------------------------------------------------------------------
// Program Entrypoint
// -------------------------------------------------------------------------
func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Critical runtime UI initialization failure: %v\n", err)
		os.Exit(1)
	}
}
