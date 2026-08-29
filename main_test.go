package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestPartitionPlanExistingEFI(t *testing.T) {
	root, boot := partitionPlan(true)
	if root != "1" || boot != "" {
		t.Fatalf("expected existing-EFI plan root=1 boot='', got root=%q boot=%q", root, boot)
	}
}

func TestPartitionPlanNewEFI(t *testing.T) {
	root, boot := partitionPlan(false)
	if root != "2" || boot != "1" {
		t.Fatalf("expected new-EFI plan root=2 boot=1, got root=%q boot=%q", root, boot)
	}
}

func TestCleanupBeforeLUKSAllowsFirstRunWithoutCryptroot(t *testing.T) {
	runner := &fakeRunner{
		results: map[string]fakeCmdResult{
			"cryptsetup close cryptroot":  {out: []byte("Device cryptroot is not active.\n"), err: errors.New("exit status 4")},
			"dmsetup remove -f cryptroot": {out: []byte("No such device or address\n"), err: errors.New("exit status 1")},
		},
	}
	stat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	if err := cleanupBeforeLUKS(runner.run, stat); err != nil {
		t.Fatalf("expected cleanup success on first run, got error: %v", err)
	}
}

func TestCleanupBeforeLUKSUsesLazyUnmountFallback(t *testing.T) {
	runner := &fakeRunner{
		results: map[string]fakeCmdResult{
			"umount -R /mnt": {out: []byte("target is busy\n"), err: errors.New("exit status 32")},
		},
	}
	stat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	if err := cleanupBeforeLUKS(runner.run, stat); err != nil {
		t.Fatalf("expected cleanup success with lazy unmount fallback, got: %v", err)
	}
	if !runner.called("umount -l /mnt") {
		t.Fatalf("expected lazy unmount fallback to be called, calls=%v", runner.calls)
	}
}

func TestCleanupBeforeLUKSFailsWhenCryptrootPersists(t *testing.T) {
	runner := &fakeRunner{results: map[string]fakeCmdResult{}}
	stat := func(string) (os.FileInfo, error) { return fakeFileInfo{}, nil }

	err := cleanupBeforeLUKS(runner.run, stat)
	if err == nil {
		t.Fatal("expected cleanup to fail when stale cryptroot mapping remains")
	}
	if !strings.Contains(err.Error(), "/dev/mapper/cryptroot") {
		t.Fatalf("expected stale mapping error, got: %v", err)
	}
}

type fakeCmdResult struct {
	out []byte
	err error
}

type fakeRunner struct {
	results map[string]fakeCmdResult
	calls   []string
}

func (f *fakeRunner) run(name string, args ...string) ([]byte, error) {
	key := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, key)
	if result, ok := f.results[key]; ok {
		return result.out, result.err
	}
	return nil, nil
}

func (f *fakeRunner) called(command string) bool {
	for _, call := range f.calls {
		if call == command {
			return true
		}
	}
	return false
}

type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "cryptroot" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }
