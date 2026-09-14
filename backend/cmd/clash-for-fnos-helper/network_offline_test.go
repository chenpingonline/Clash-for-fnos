package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

func offlineNetworkHelper(t *testing.T) *helper {
	h := testHelper(t)
	if err := atomicWrite(h.config.managedCore, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	raw := "mixed-port: 7890\nexternal-controller: 127.0.0.1:9191\nsecret: keep-secret\n# keep user rules\nrules:\n  - MATCH,DIRECT\n"
	if err := atomicWrite(h.config.managedConfig, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return h
}
func TestOfflineNetworkReadSaveAndRollback(t *testing.T) {
	h := offlineNetworkHelper(t)
	ctx := context.Background()
	status, err := h.networkStatus(ctx)
	if err != nil || status["offline"] != true {
		t.Fatalf("status=%v err=%v", status, err)
	}
	prepared, err := h.updateNetwork(ctx, map[string]any{"mixed": map[string]any{"enabled": true, "port": float64(7891)}})
	if err != nil {
		t.Fatal(err)
	}
	if prepared["controller"].(map[string]any)["clientUrl"] != "http://127.0.0.1:9191" {
		t.Fatal("unrelated save reset controller")
	}
	if changed, _ := prepared["controllerChanged"].(bool); changed {
		t.Fatal("unrelated save reported controller change")
	}
	id := prepared["txId"].(string)
	activated, err := h.activateConfig(ctx, id)
	if err != nil || activated["method"] != "saved-only" {
		t.Fatalf("activation=%v err=%v", activated, err)
	}
	raw, _ := os.ReadFile(h.config.managedConfig)
	for _, text := range []string{"mixed-port: 7891", "secret: keep-secret", "# keep user rules", "  - MATCH,DIRECT"} {
		if !strings.Contains(string(raw), text) {
			t.Fatalf("lost %s in %s", text, raw)
		}
	}
	if _, err := h.rollbackConfig(ctx, id); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(h.config.managedConfig)
	if !strings.Contains(string(raw), "mixed-port: 7890") {
		t.Fatal("rollback failed")
	}
}

func TestOfflineNetworkReportsControllerChange(t *testing.T) {
	h := offlineNetworkHelper(t)
	prepared, err := h.updateNetwork(context.Background(), map[string]any{
		"controller": map[string]any{"enabled": true, "port": float64(49151)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed, _ := prepared["controllerChanged"].(bool); !changed {
		t.Fatal("controller port change was not reported")
	}
	if got := prepared["controller"].(map[string]any)["clientUrl"]; got != "http://127.0.0.1:49151" {
		t.Fatalf("controller = %v", got)
	}
}

func TestDisabledTunOnlySaveDefersRuntimeActivation(t *testing.T) {
	tests := []struct {
		name                     string
		input                    map[string]any
		previousEnabled, enabled bool
		want                     bool
	}{
		{"disabled TUN parameters", map[string]any{"tun": map[string]any{"mtu": float64(1400)}}, false, false, true},
		{"TUN is running", map[string]any{"tun": map[string]any{"mtu": float64(1400)}}, true, true, false},
		{"enabling TUN", map[string]any{"tun": map[string]any{"enabled": true}}, false, true, false},
		{"mixed network patch", map[string]any{"tun": map[string]any{"mtu": float64(1400)}, "allowLan": true}, false, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldSaveDisabledTunOnly(test.input, test.previousEnabled, test.enabled); got != test.want {
				t.Fatalf("shouldSaveDisabledTunOnly()=%v want %v", got, test.want)
			}
		})
	}

	h := testHelper(t)
	target := h.config.managedConfig
	if err := atomicWrite(target, []byte("tun: {enable: false, mtu: 1500}\n"), 0640); err != nil {
		t.Fatal(err)
	}
	candidate := target + ".candidate"
	if err := atomicWrite(candidate, []byte("tun: {enable: false, mtu: 1400}\n"), 0640); err != nil {
		t.Fatal(err)
	}
	h.transactions["tun-preconfigure"] = &transaction{Target: target, Candidate: candidate, SaveOnlyReason: "tun-disabled", Mode: 0640, UID: -1, GID: -1}
	result, err := h.activateConfig(context.Background(), "tun-preconfigure")
	if err != nil || result["method"] != "saved-only" || result["reason"] != "tun-disabled" {
		t.Fatalf("activation=%v err=%v", result, err)
	}
	body, _ := os.ReadFile(target)
	if !strings.Contains(string(body), "mtu: 1400") {
		t.Fatalf("TUN preconfiguration was not saved: %s", body)
	}
}

func TestOfflineNetworkRejectsInvalidPortsAndValidationFailure(t *testing.T) {
	h := offlineNetworkHelper(t)
	ctx := context.Background()
	before, _ := os.ReadFile(h.config.managedConfig)
	for _, port := range []any{float64(0), float64(65536), float64(12.5), "7891"} {
		if _, err := h.updateNetwork(ctx, map[string]any{"mixed": map[string]any{"enabled": true, "port": port}}); err == nil {
			t.Fatalf("accepted %v", port)
		}
	}
	if err := atomicWrite(h.config.managedCore, []byte("#!/bin/sh\necho invalid\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.updateNetwork(ctx, map[string]any{"controller": map[string]any{"enabled": true, "port": float64(9192)}}); err == nil {
		t.Fatal("accepted invalid config")
	}
	after, _ := os.ReadFile(h.config.managedConfig)
	if string(before) != string(after) {
		t.Fatal("failed validation changed original")
	}
}
func TestOfflineNetworkNeverFallsBackForExternalMode(t *testing.T) {
	h := offlineNetworkHelper(t)
	if err := h.writeMode("external"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.networkStatus(context.Background()); err == nil {
		t.Fatal("external mode read managed config")
	}
}
