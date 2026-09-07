package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHelper(t *testing.T) *helper {
	t.Helper()
	root := t.TempDir()
	cfg := helperConfig{
		etcDir: root, varDir: root, appDir: root,
		managedCoreDir: filepath.Join(root, "managed-core"), managedCore: filepath.Join(root, "managed-core", "mihomo"), managedPID: filepath.Join(root, "managed-core", "mihomo.pid"), managedLog: filepath.Join(root, "managed-core", "mihomo.log"),
		managedConfigDir: filepath.Join(root, "mihomo"), managedConfig: filepath.Join(root, "mihomo", "config.yaml"), coreModeFile: filepath.Join(root, "core-mode.json"), stageDir: filepath.Join(root, "core-stage"), backupDir: filepath.Join(root, "backups"), proxySettingsFile: filepath.Join(root, "proxy.json"), iconSettingsFile: filepath.Join(root, "icon.json"),
	}
	return newHelper(cfg)
}

func TestConfigTransactionWritesOnlyManagedTarget(t *testing.T) {
	h := testHelper(t)
	original := "mixed-port: 7890\n"
	if err := atomicWrite(h.config.managedConfig, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := h.prepareConfig(context.Background(), "mixed-port: 7891\n")
	if err != nil {
		t.Fatal(err)
	}
	txID := prepared["txId"].(string)
	if _, err = h.activateConfig(context.Background(), txID); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(h.config.managedConfig)
	if err != nil || string(body) != "mixed-port: 7891\n" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	if _, err = h.rollbackConfig(context.Background(), txID); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(h.config.managedConfig)
	if string(body) != original {
		t.Fatalf("rollback body=%q", body)
	}
}

func TestHelperRejectsUnknownRoutesAndUnsafePaths(t *testing.T) {
	h := testHelper(t)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/command", strings.NewReader(`{"command":"id"}`)))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := h.readPath("/etc/passwd"); err == nil {
		t.Fatal("expected arbitrary path rejection")
	}
}

func TestManagedProxyBlockIsIdempotentAndPreservesOtherContent(t *testing.T) {
	settings := defaultProxySettings()
	original := "LANG=en_US.UTF-8\n"
	first, err := withProxyBlock(original, proxyBlock(settings, false))
	if err != nil {
		t.Fatal(err)
	}
	second, err := withProxyBlock(first, proxyBlock(settings, false))
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.Contains(first, original) {
		t.Fatalf("managed block is not idempotent:\n%s", second)
	}
	clean, err := stripProxyBlock(second)
	if err != nil || strings.TrimSpace(clean) != strings.TrimSpace(original) {
		t.Fatalf("clean=%q err=%v", clean, err)
	}
}

func TestConfigAPIContractOverHTTP(t *testing.T) {
	h := testHelper(t)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/config/sync", strings.NewReader(`{"content":"port: 7899\n"}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["txId"] == nil || response["target"] != h.config.managedConfig {
		t.Fatalf("response=%#v", response)
	}
}

func TestDNSAndTunRenderingUsesMihomoKeys(t *testing.T) {
	dns, hosts := normalizeDNSForYAML(map[string]any{
		"enable": true, "enhancedMode": "fake-ip", "fallbackGeoip": true, "fallbackGeoipCode": "CN",
		"fallbackIpCidr": []any{"240.0.0.0/4"}, "fallbackDomain": []any{"+.example.com"},
		"nameserverPolicy": []any{map[string]any{"matcher": "+.example.com", "servers": []any{"1.1.1.1"}}},
		"hosts":            []any{map[string]any{"host": "nas.local", "values": []any{"192.168.1.2"}}},
	})
	rendered := renderYAML("dns", dns) + "\n" + renderYAML("hosts", hostMap(hosts)) + "\n" + renderYAML("tun", map[string]any{"autoRoute": true, "dnsHijack": true})
	for _, expected := range []string{"enhanced-mode:", "fallback-filter:", "nameserver-policy:", "nas.local:", "auto-route:", "dns-hijack:"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("missing %q in:\n%s", expected, rendered)
		}
	}
}
