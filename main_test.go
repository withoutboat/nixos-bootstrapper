package main

import (
	"strings"
	"testing"
)

func TestInjectRuntimeSpecIncludesWiFiData(t *testing.T) {
	base := "{\n}\n"
	spec := runtimeSpec{
		username:   "alice",
		cpu:        "intel",
		gpu:        "nvidia",
		nvidiaOpen: true,
		wifiSSID:   `Cafe "WiFi"`,
		wifiPass:   `pa\ss${word}`,
	}

	got := injectRuntimeSpec(base, spec, "PCI:0:2:0", "PCI:1:0:0")

	for _, want := range []string{
		`wifiSSID = "Cafe \"WiFi\""`,
		`wifiPass = "pa\\ss\${word}"`,
		`username = "alice"`,
		`intelBusId = "PCI:0:2:0"`,
		`nvidiaBusId = "PCI:1:0:0"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected injected config to contain %q, got:\n%s", want, got)
		}
	}
}

func TestInjectRuntimeSpecWithoutClosingBraceLeavesConfigUnchanged(t *testing.T) {
	base := "{\n"

	if got := injectRuntimeSpec(base, runtimeSpec{}, "", ""); got != base {
		t.Fatalf("expected unchanged config, got %q", got)
	}
}
