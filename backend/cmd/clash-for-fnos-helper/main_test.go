package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestManagedProxyBlockRemovesLegacyAndCurrentBlocks(t *testing.T) {
	original := "PATH=/usr/bin\n"
	legacy := legacyProxyBegin + "\nexport HTTP_PROXY=\"http://127.0.0.1:7890\"\n" + legacyProxyEnd + "\n"
	current := proxyBlock(defaultProxySettings(), true) + "\n"
	clean, err := stripProxyBlock(original + legacy + current)
	if err != nil {
		t.Fatal(err)
	}
	if clean != original {
		t.Fatalf("clean=%q", clean)
	}
	quotedMarker := "echo \"# >>> Clash for fnos proxy >>>\"\n"
	if clean, err = stripProxyBlock(quotedMarker); err != nil || clean != quotedMarker {
		t.Fatalf("quoted marker changed: clean=%q err=%v", clean, err)
	}
}

func TestManagedProxyBlockRejectsMalformedLegacyMarkers(t *testing.T) {
	for _, raw := range []string{legacyProxyBegin + "\nHTTP_PROXY=x\n", legacyProxyEnd + "\n", proxyBegin + "\n" + legacyProxyEnd + "\n"} {
		if _, err := stripProxyBlock(raw); err == nil {
			t.Fatalf("expected malformed block rejection for %q", raw)
		}
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

func TestConfigSyncCanReuseLiveApplyValidation(t *testing.T) {
	h := testHelper(t)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/config/sync", strings.NewReader(`{"content":"port: 7899\n","skipValidation":true}`)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Validation map[string]any `json:"validation"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Validation["method"] != "live-apply" || response.Validation["skipped"] != true {
		t.Fatalf("validation=%#v", response.Validation)
	}
}

func TestDNSAndTunRenderingUsesMihomoKeys(t *testing.T) {
	dns, hosts := normalizeDNSForYAML(map[string]any{
		"enable": true, "enhancedMode": "fake-ip", "fallbackGeoip": true, "fallbackGeoipCode": "CN",
		"fallbackIpCidr": []any{"240.0.0.0/4"}, "fallbackDomain": []any{"+.example.com"},
		"nameserverPolicy": []any{map[string]any{"matcher": "+.example.com", "servers": []any{"1.1.1.1"}}},
		"hosts":            []any{map[string]any{"host": "nas.local", "values": []any{"192.168.1.2"}}},
	})
	tun, err := normalizeTunForYAML(map[string]any{"enabled": true, "autoRoute": true, "dnsHijack": true, "routeExcludeAddress": []any{"192.168.1.9/24", "fc00::/7"}})
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderYAML("dns", dns) + "\n" + renderYAML("hosts", hostMap(hosts)) + "\n" + renderYAML("tun", tun)
	for _, expected := range []string{"enhanced-mode:", "fallback-filter:", "nameserver-policy:", "nas.local:", "enable: true", "auto-route:", "dns-hijack:", `- "any:53"`, "route-exclude-address:", `- "192.168.1.0/24"`, `- "fc00::/7"`} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("missing %q in:\n%s", expected, rendered)
		}
	}
}

func TestTunRouteExcludeAddressValidationAndReading(t *testing.T) {
	if _, err := normalizeTunForYAML(map[string]any{"routeExcludeAddress": []any{"not-a-cidr"}}); err == nil {
		t.Fatal("expected invalid CIDR rejection")
	}
	raw := "tun:\n  mtu: 1500\n  route-exclude-address:\n    - 192.168.0.0/16\n    - 'fc00::/7'\n  auto-route: true\n"
	items := yamlNestedStringList(raw, "tun", "route-exclude-address")
	if len(items) != 2 || items[0] != "192.168.0.0/16" || items[1] != "fc00::/7" {
		t.Fatalf("items=%#v", items)
	}
}

func TestTunCapabilityRequiresDeviceAndPermission(t *testing.T) {
	managed := &processInfo{Managed: true}
	external := &processInfo{Managed: false}
	tests := []struct {
		name       string
		proc       *processInfo
		tunDevice  bool
		effective  int
		supported  bool
		reasonCode string
	}{
		{name: "managed core", proc: managed, tunDevice: true, effective: 1000, supported: true},
		{name: "root external core", proc: external, tunDevice: true, effective: 0, supported: true},
		{name: "missing device", proc: managed, tunDevice: false, effective: 1000, reasonCode: "tun-device-missing"},
		{name: "core stopped", tunDevice: true, effective: 0, reasonCode: "core-not-running"},
		{name: "external core without permission", proc: external, tunDevice: true, effective: 1000, reasonCode: "permission-denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capability := resolveTunCapability(test.proc, test.tunDevice, test.effective)
			if capability["supported"] != test.supported || capability["reason"] != test.reasonCode {
				t.Fatalf("capability=%#v", capability)
			}
		})
	}
}

func TestBundledCoreUsesBuildMetadataAndVerifiesDigest(t *testing.T) {
	h := testHelper(t)
	coreDir := filepath.Join(h.config.appDir, "core")
	if err := os.MkdirAll(coreDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte("#!/bin/sh\necho 'Mihomo Meta v1.19.30'\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(compressed.Bytes())
	asset := "mihomo-linux-test-v1.19.30.gz"
	if err := os.WriteFile(filepath.Join(coreDir, asset), compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"tag": "v1.19.30", "size": compressed.Len(), "sha256": hex.EncodeToString(sum[:])}
	metaBody, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(coreDir, "bundled-core.json"), metaBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.installBundled(); err != nil {
		t.Fatal(err)
	}
	if readVersion(h.config.managedCore) != "v1.19.30" {
		t.Fatalf("unexpected installed version: %s", readVersion(h.config.managedCore))
	}
}

func TestTerminateManagedPIDAllowsGracefulExit(t *testing.T) {
	command := exec.Command("sh", "-c", "trap 'exit 0' TERM; while :; do sleep 0.05; done")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill() })
	time.Sleep(100 * time.Millisecond)
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()

	started := time.Now()
	terminateManagedPID(command.Process.Pid, 2*time.Second)
	if err := <-waited; err != nil {
		t.Fatalf("managed process did not exit gracefully: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("graceful shutdown reached force-kill timeout: %s", elapsed)
	}
}
