package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestRulesEndpointUsesDiskSnapshotUntilExplicitRefresh(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rules" {
			t.Fatalf("unexpected controller path: %s", r.URL.Path)
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"rules":[{"type":"DOMAIN","payload":"live.example","proxy":"DIRECT"}]}`)
	}))
	defer controller.Close()

	root := t.TempDir()
	snapshotFile := filepath.Join(root, "rules-snapshot.json")
	diskPayload := []byte(`{"rules":[{"type":"DOMAIN","payload":"disk.example","proxy":"DIRECT"}]}`)
	if err := os.WriteFile(snapshotFile, diskPayload, 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newGateway(config{
		publicDir:         root,
		gateway:           "/app/clash-for-fnos",
		settingsFile:      writeGatewaySettings(t, controller.URL),
		rulesSnapshotFile: snapshotFile,
	})

	diskRecorder := httptest.NewRecorder()
	handler.ServeHTTP(diskRecorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/rules", nil))
	if diskRecorder.Code != http.StatusOK || diskRecorder.Header().Get("X-Clash-Data-Source") != "disk" {
		t.Fatalf("disk response status=%d source=%q body=%s", diskRecorder.Code, diskRecorder.Header().Get("X-Clash-Data-Source"), diskRecorder.Body.String())
	}
	if requests.Load() != 0 || diskRecorder.Body.String() != string(diskPayload) {
		t.Fatalf("disk snapshot was not served directly: requests=%d body=%s", requests.Load(), diskRecorder.Body.String())
	}

	liveRecorder := httptest.NewRecorder()
	handler.ServeHTTP(liveRecorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/rules?refresh=1", nil))
	if liveRecorder.Code != http.StatusOK || liveRecorder.Header().Get("X-Clash-Data-Source") != "mihomo" || requests.Load() != 1 {
		t.Fatalf("live response status=%d source=%q requests=%d body=%s", liveRecorder.Code, liveRecorder.Header().Get("X-Clash-Data-Source"), requests.Load(), liveRecorder.Body.String())
	}
	stored, err := os.ReadFile(snapshotFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != liveRecorder.Body.String() {
		t.Fatalf("refreshed snapshot mismatch: stored=%s response=%s", stored, liveRecorder.Body.String())
	}
}

func TestConditionalJSONReturnsNotModifiedForMatchingETag(t *testing.T) {
	t.Parallel()
	first := httptest.NewRecorder()
	writeConditionalJSON(first, httptest.NewRequest(http.MethodGet, "/config", nil), map[string]string{"content": "mixed-port: 7890"})
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" {
		t.Fatalf("first response status=%d etag=%q", first.Code, etag)
	}

	request := httptest.NewRequest(http.MethodGet, "/config", nil)
	request.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	writeConditionalJSON(second, request, map[string]string{"content": "mixed-port: 7890"})
	if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
		t.Fatalf("conditional response status=%d body=%q", second.Code, second.Body.String())
	}
}
