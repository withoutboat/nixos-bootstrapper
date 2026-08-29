package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderRuntimeConfigurationIncludesRuntimeData(t *testing.T) {
	spec := runtimeSpec{
		username:   "alice",
		cpu:        "intel",
		gpu:        "nvidia",
		nvidiaOpen: true,
		wifiSSID:   `Cafe "WiFi"`,
		wifiPass:   `pa\ss${word}`,
	}

	got := renderRuntimeConfiguration(spec, "PCI:0:2:0", "PCI:1:0:0")

	for _, want := range []string{
		`wifiSSID = "Cafe \"WiFi\""`,
		`wifiPass = "pa\\ss\${word}"`,
		`username = "alice"`,
		`cpu = "intel"`,
		`gpu = "nvidia"`,
		`nvidiaOpen = true`,
		`intelBusId = "PCI:0:2:0"`,
		`nvidiaBusId = "PCI:1:0:0"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected injected config to contain %q, got:\n%s", want, got)
		}
	}
}

func TestRenderRuntimeConfigurationDoesNotMutateHardwareContent(t *testing.T) {
	hardware := "{\n  fileSystems.\"/\" = { };\n}\n"
	rendered := renderRuntimeConfiguration(runtimeSpec{username: "alice"}, "PCI:0:2:0", "PCI:1:0:0")

	if !strings.Contains(rendered, `_module.args.spec = {`) {
		t.Fatalf("expected configuration output to contain runtime spec, got:\n%s", rendered)
	}
	if strings.Contains(hardware, `_module.args.spec = {`) {
		t.Fatalf("expected hardware fixture to remain runtime-free, got:\n%s", hardware)
	}
}

func TestBuildHostPathsUsesHostsDirectory(t *testing.T) {
	got, err := buildHostPaths("/tmp/nix-core", "pc-th")
	if err != nil {
		t.Fatalf("buildHostPaths returned error: %v", err)
	}

	wantDir := filepath.Join("/tmp/nix-core", "hosts", "pc-th")
	if got.Dir != wantDir {
		t.Fatalf("expected dir %q, got %q", wantDir, got.Dir)
	}
	if got.Default != filepath.Join(wantDir, "default.nix") {
		t.Fatalf("unexpected default path: %q", got.Default)
	}
	if got.Hardware != filepath.Join(wantDir, "hardware.nix") {
		t.Fatalf("unexpected hardware path: %q", got.Hardware)
	}
	if got.Configuration != filepath.Join(wantDir, "configuration.nix") {
		t.Fatalf("unexpected configuration path: %q", got.Configuration)
	}
}

func TestValidateHostNameRejectsTraversal(t *testing.T) {
	for _, host := range []string{"../pc-th", "pc/th", "", "pc th"} {
		if err := validateHostName(host); err == nil {
			t.Fatalf("expected host %q to be rejected", host)
		}
	}
}

func TestVerifyU2FMappingWritten(t *testing.T) {
	content := []byte("example-u2f-mapping-line\n")
	trimmed := bytes.TrimSpace(content)

	if len(trimmed) == 0 {
		t.Fatal("expected non-empty trimmed content")
	}

	if !bytes.Equal(trimmed, bytes.TrimSpace(content)) {
		t.Fatal("expected trimmed content to match")
	}
}
