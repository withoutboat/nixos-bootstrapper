package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	registryPath = "/etc/nixos-source-registry"
	targetMnt    = "/mnt/etc/nixos"
)

func main() {
	fmt.Println("🚀 START: Automated NixOS Installation Framework")

	// TODO: Add disk discovery, partitioning (parted), LUKS, and filesystems orchestration here

	// 1. Validate installation source inside the bootable ISO environment
	localConfig := filepath.Join(registryPath, "nixos-config")
	if _, err := os.Stat(localConfig); os.IsNotExist(err) {
		fmt.Printf("❌ Critical Error: Base configuration source not found at %s\n", localConfig)
		os.Exit(1)
	}

	// 2. Prepare target disk layout directory and sync public configuration
	fmt.Println("📦 Syncing system blueprints to target storage target...")
	runCmd("mkdir", "-p", targetMnt)
	runCmd("cp", "-r", localConfig, filepath.Join(targetMnt, "nixos-config"))

	// 3. Inspect local registry for private workspace configurations overlay
	privateEnv := filepath.Join(registryPath, "nix-home-work")
	hasPrivateEnv := false

	if _, err := os.Stat(privateEnv); err == nil {
		fmt.Println("🔒 Private workspace overlay detected (nix-home-work). Integrating local mirror...")
		runCmd("cp", "-r", privateEnv, filepath.Join(targetMnt, "nix-home-work"))
		hasPrivateEnv = true
	} else {
		fmt.Println("ℹ️ No private workspace overlay detected. Proceeding with standard baseline deployment.")
	}

	// 4. Reset permissions on the copied directory to allow the Nix build daemon write access
	runCmd("chmod", "-R", "u+w", targetMnt)

	// 5. Construct execution payload for target system build
	flakeTarget := fmt.Sprintf("%s/nixos-config#workstation", targetMnt)
	cmdArgs := []string{
		"nixos-install",
		"--flake", flakeTarget,
		"--no-root-passwd",
	}

	// 6. Force input interception if private workspace environment is mapped locally
	if hasPrivateEnv {
		localPrivatePath := filepath.Join(targetMnt, "nix-home-work")
		cmdArgs = append(cmdArgs, "--override-input", "nix-home-work", localPrivatePath)
	}

	// 7. Execute installation loop
	fmt.Println("⚙️ Invoking system builder engine (nixos-install)...")
	runCmd(cmdArgs[0], cmdArgs[1:]...)

	fmt.Println("🎉 Installation completed successfully. Ready for reboot.")
}

func runCmd(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("❌ Command execution failure [%s]: %v\n", name, err)
		os.Exit(1)
	}
}
