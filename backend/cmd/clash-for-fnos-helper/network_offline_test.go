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
