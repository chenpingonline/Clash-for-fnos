package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeoUpdateUsesMihomoUpgradeEndpoint(t *testing.T) {
	t.Parallel()
	helperCalls, upgradeCalls := 0, 0
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/geo/status" {
			http.NotFound(w, r)
			return
		}
		helperCalls++
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "managed", "canUpdate": true, "readOnly": false, "assets": []any{}})
	}))
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/upgrade/geo" {
			upgradeCalls++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer controller.Close()

	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: helperSocket})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/geo/update", nil))
	if recorder.Code != http.StatusOK || upgradeCalls != 1 || helperCalls != 2 {
		t.Fatalf("status=%d helperCalls=%d upgradeCalls=%d body=%s", recorder.Code, helperCalls, upgradeCalls, recorder.Body.String())
	}
}

func TestGeoUpdateRejectsExternalMode(t *testing.T) {
	t.Parallel()
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "external", "canUpdate": false, "readOnly": true, "message": "External 模式只读"})
	}))
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", privilegedSocket: helperSocket})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/geo/update", nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGeoDownloadForwardsSelectedAssetToHelper(t *testing.T) {
	t.Parallel()
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/geo/download" {
			http.NotFound(w, r)
			return
		}
		var payload map[string]any
		if !decodeJSONBody(w, r, &payload) || payload["key"] != "asn" {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "assetKey": "asn", "assets": []any{map[string]any{"key": "asn", "present": true}}})
	}))
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", privilegedSocket: helperSocket})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/geo/download", strings.NewReader(`{"key":"asn"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"assetKey":"asn"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
