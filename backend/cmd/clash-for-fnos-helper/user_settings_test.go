package main

import (
	"context"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"strings"
	"testing"
)

func TestUserSettingsPersistAcrossSubscriptionsAndRollback(t *testing.T) {
	h := offlineNetworkHelper(t)
	ctx := context.Background()
	prepared, err := h.updateNetwork(ctx, map[string]any{"core": map[string]any{"ipv6": false, "unifiedDelay": true}, "tun": map[string]any{"mtu": float64(1400)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(h.userSettingsPath()); !os.IsNotExist(err) {
		t.Fatal("preferences saved before activation")
	}
	id := prepared["txId"].(string)
	if _, err = h.activateConfig(ctx, id); err != nil {
		t.Fatal(err)
	}
	h.commitConfig(id)
	// A new helper instance reads preferences from disk, without relying on memory.
	restarted := &helper{config: h.config}
	source := "ipv6: true\nunified-delay: false\nallow-lan: true\ntun: {enable: false, mtu: 9000, stack: system}\nproxies: []\nrules: [MATCH,DIRECT]\n"
	result, err := restarted.composeUserSettings(map[string]any{"content": source})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	yaml.Unmarshal([]byte(result["content"].(string)), &got)
	if got["ipv6"] != false || got["unified-delay"] != true || got["allow-lan"] != true || got["tun"].(map[string]any)["mtu"] != 1400 || got["tun"].(map[string]any)["stack"] != "system" {
		t.Fatalf("%v", got)
	}
	original, _ := os.ReadFile(h.userSettingsPath())
	prepared, err = h.updateNetwork(ctx, map[string]any{"core": map[string]any{"ipv6": true}})
	if err != nil {
		t.Fatal(err)
	}
	id = prepared["txId"].(string)
	if _, err = h.activateConfig(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err = h.rollbackConfig(ctx, id); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(h.userSettingsPath())
	if string(restored) != string(original) {
		t.Fatal("preferences not rolled back")
	}
}
func TestUserSettingsDNSAndGeoComposition(t *testing.T) {
	h := offlineNetworkHelper(t)
	prepared, err := h.prepareConfig(context.Background(), "rules: []\n")
	if err != nil {
		t.Fatal(err)
	}
	h.attachUserSettings(prepared, map[string]any{"geo-auto-update": true, "geo-update-interval": float64(72), "tun": map[string]any{"enable": false}})
	if _, err = h.activateConfig(context.Background(), prepared["txId"].(string)); err != nil {
		t.Fatal(err)
	}
	source := "geo-auto-update: false\ngeo-update-interval: 24\ntun: {enable: true}\ndns: {enable: false}\n"
	for _, enabled := range []bool{true, false} {
		result, err := h.composeUserSettings(map[string]any{"content": source, "dnsOverrideEnabled": enabled, "dns": map[string]any{"enable": true, "nameserver": []any{"1.1.1.1"}, "hosts": []any{}}})
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		yaml.Unmarshal([]byte(result["content"].(string)), &got)
		if got["geo-auto-update"] != true || got["geo-update-interval"] != 72 || got["tun"].(map[string]any)["enable"] != false || got["dns"].(map[string]any)["enable"] != enabled {
			t.Fatalf("%v", got)
		}
	}
}
func TestUserSettingsFailureDoesNotPersist(t *testing.T) {
	h := offlineNetworkHelper(t)
	atomicWrite(h.config.managedCore, []byte("#!/bin/sh\nexit 1\n"), 0755)
	if _, err := h.updateNetwork(context.Background(), map[string]any{"core": map[string]any{"ipv6": false}}); err == nil {
		t.Fatal("expected validation failure")
	}
	if _, err := os.Stat(h.userSettingsPath()); !os.IsNotExist(err) {
		t.Fatal("invalid preference persisted")
	}
	atomicWrite(h.userSettingsPath(), []byte("broken"), 0600)
	if _, err := h.composeUserSettings(map[string]any{"content": "rules: []\n"}); err == nil || !strings.Contains(err.Error(), "用户内核设置") {
		t.Fatalf("%v", err)
	}
}

func TestUserPortOverridesPreserveDisabledAndUntouchedFields(t *testing.T) {
	patch, err := normalizeUserPatch(map[string]any{
		"controller": map[string]any{"port": float64(9191)},
		"mixed":      map[string]any{"enabled": false, "port": float64(7890)},
		"allowLan":   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := offlineNetworkHelper(t)
	body, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	if err = atomicWrite(h.userSettingsPath(), body, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := h.composeUserSettings(map[string]any{"content": "external-controller: 127.0.0.1:9090\nmixed-port: 7890\nsocks-port: 7898\nallow-lan: true\n"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = yaml.Unmarshal([]byte(result["content"].(string)), &got); err != nil {
		t.Fatal(err)
	}
	if got["external-controller"] != "127.0.0.1:9191" || got["mixed-port"] != 0 || got["socks-port"] != 7898 || got["allow-lan"] != false {
		t.Fatalf("%v", got)
	}
}
