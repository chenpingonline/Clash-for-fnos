package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyGeoSettingsPreservesCustomSourceAndOtherConfig(t *testing.T) {
	raw := "mixed-port: 7890\ngeo-auto-update: false\ngeo-update-interval: 48\ngeox-url:\n  geoip: https://example.com/custom-geoip.dat\nrules:\n  - MATCH,DIRECT\n"
	result := applyGeoSettings(raw, true, 168)
	for _, expected := range []string{
		"mixed-port: 7890",
		"rules:\n  - MATCH,DIRECT",
		"geo-auto-update: true",
		"geo-update-interval: 168",
		"geoip: \"https://example.com/custom-geoip.dat\"",
		"geosite: \"https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geosite.dat\"",
		"mmdb: \"https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/country.mmdb\"",
		"asn: \"https://github.com/xishang0128/geoip/releases/download/latest/GeoLite2-ASN.mmdb\"",
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("expected rendered config to contain %q:\n%s", expected, result)
		}
	}
	if strings.Count(result, "geo-auto-update:") != 1 || strings.Count(result, "geox-url:") != 1 {
		t.Fatalf("GEO keys must be replaced idempotently:\n%s", result)
	}
}

func TestGeoSettingsDefaults(t *testing.T) {
	auto, interval, urls := geoSettingsFromConfig("mixed-port: 7890\n")
	if auto || interval != 24 || len(urls) != 4 {
		t.Fatalf("unexpected defaults: auto=%v interval=%d urls=%v", auto, interval, urls)
	}
}

func TestDownloadGeoAssetFileRejectsPlainHTTPWithoutWriting(t *testing.T) {
	payload := append([]byte(strings.Repeat("x", 2048)), []byte("\xab\xcd\xefMaxMind.com")...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "ASN.mmdb")
	asset, _ := geoAssetByKey("asn")
	if err := downloadGeoAssetFile(context.Background(), asset, server.URL, target); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected HTTP source to be rejected, got %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("unsafe download must not create target: %v", err)
	}
}

func TestValidateGeoAssetRejectsHTMLAndInvalidMMDB(t *testing.T) {
	asset, _ := geoAssetByKey("asn")
	if err := validateGeoAsset(asset, []byte(strings.Repeat("<html>", 200)), "text/html"); err == nil {
		t.Fatal("expected HTML response to be rejected")
	}
	if err := validateGeoAsset(asset, []byte(strings.Repeat("x", 2048)), "application/octet-stream"); err == nil {
		t.Fatal("expected invalid MMDB to be rejected")
	}
}
