package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOfflineNetworkSaveDoesNotCallController(t *testing.T) {
	var requests atomic.Int32
	var proxySyncs atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(503) }))
	defer controller.Close()
	socket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/network/update":
			writeJSON(w, 200, map[string]any{"txId": "offline", "controller": map[string]any{"clientUrl": controller.URL}, "settings": map[string]any{}, "validation": map[string]any{"ok": true}})
		case "/config/activate":
			writeJSON(w, 200, map[string]any{"method": "saved-only"})
		case "/config/commit":
			writeJSON(w, 200, map[string]any{"ok": true})
		case "/system/proxy-environment/sync":
			proxySyncs.Add(1)
			writeJSON(w, 200, map[string]any{"management": map[string]any{"settings": map[string]any{"followMixedPort": true, "port": 9192}}})
		case "/status":
			writeJSON(w, 200, map[string]any{"mode": "managed", "managedController": controller.URL, "managedSecret": "new-secret"})
		default:
			t.Errorf("unexpected helper request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	settings := writeGatewaySettings(t, controller.URL)
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", settingsFile: settings, privilegedSocket: socket})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/network/settings", strings.NewReader(`{"controller":{"enabled":true,"port":9192}}`)))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "saved-only") {
		t.Fatalf("status=%d %s", w.Code, w.Body.String())
	}
	if requests.Load() != 0 {
		t.Fatal("offline save attempted runtime request")
	}
	if proxySyncs.Load() != 1 || !strings.Contains(w.Body.String(), `"port":9192`) {
		t.Fatalf("offline save did not sync proxy environment: syncs=%d body=%s", proxySyncs.Load(), w.Body.String())
	}
	raw, _ := os.ReadFile(settings)
	var saved map[string]any
	_ = json.Unmarshal(raw, &saved)
	if saved["controller"] != controller.URL || saved["secret"] != "new-secret" {
		t.Fatalf("controller not synced: %s", raw)
	}
}

func TestNetworkApplyFailureRollsBackAndReportsProgress(t *testing.T) {
	var patches atomic.Int32
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			if patches.Add(1) == 1 {
				writeJSON(w, 500, map[string]string{"message": "listener failed"})
				return
			}
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(404)
	}))
	defer controller.Close()
	var rolledBack atomic.Bool
	var handler http.Handler
	socket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/network/update":
			status := httptest.NewRecorder()
			handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/network/settings/status", nil))
			if !strings.Contains(status.Body.String(), `"id":"save-1"`) || !strings.Contains(status.Body.String(), "正在检查端口") {
				t.Errorf("missing correlated progress: %s", status.Body.String())
			}
			writeJSON(w, 200, map[string]any{"txId": "tx", "previousContent": "mixed-port: 7890\n", "effectiveContent": "mixed-port: 7891\n"})
		case "/config/activate":
			writeJSON(w, 200, map[string]any{"method": "hot-reload"})
		case "/config/rollback":
			rolledBack.Store(true)
			writeJSON(w, 200, map[string]any{"ok": true})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	handler = newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: socket})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/network/settings", strings.NewReader(`{"mixed":{"enabled":true,"port":7891}}`))
	req.Header.Set("X-Network-Operation", "save-1")
	handler.ServeHTTP(w, req)
	if w.Code != 502 || !rolledBack.Load() || patches.Load() != 2 || !strings.Contains(w.Body.String(), "已回滚") {
		t.Fatalf("status=%d body=%s patches=%d rollback=%v", w.Code, w.Body.String(), patches.Load(), rolledBack.Load())
	}
}

func TestControllerPortChangeRestartsManagedCore(t *testing.T) {
	var oldControllerRequests atomic.Int32
	oldController := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oldControllerRequests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer oldController.Close()

	newController := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			writeJSON(w, http.StatusOK, map[string]any{"version": "test"})
		case "/configs":
			writeJSON(w, http.StatusOK, map[string]any{})
		default:
			t.Errorf("unexpected controller request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer newController.Close()

	var restarts atomic.Int32
	var commits atomic.Int32
	socket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/network/update":
			writeJSON(w, http.StatusOK, map[string]any{
				"txId": "controller-port", "controllerChanged": true,
				"controller": map[string]any{"clientUrl": newController.URL},
				"settings":   map[string]any{"controller": map[string]any{"enabled": true, "port": float64(9097)}},
				"validation": map[string]any{"ok": true},
			})
		case "/status":
			writeJSON(w, http.StatusOK, map[string]any{
				"mode": "managed", "canRestartService": true,
				"managedController": newController.URL, "managedSecret": "new-secret",
			})
		case "/config/activate":
			writeJSON(w, http.StatusOK, map[string]any{"method": "hot-reload"})
		case "/core/restart-managed":
			restarts.Add(1)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		case "/config/commit":
			commits.Add(1)
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		case "/system/proxy-environment/sync":
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			t.Errorf("unexpected helper request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))

	settings := writeGatewaySettings(t, oldController.URL)
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", settingsFile: settings, privilegedSocket: socket})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/network/settings", strings.NewReader(`{"controller":{"enabled":true,"port":9097}}`)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"activation":"managed-restart"`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if restarts.Load() != 1 || commits.Load() != 1 {
		t.Fatalf("restarts=%d commits=%d", restarts.Load(), commits.Load())
	}
	if oldControllerRequests.Load() != 0 {
		t.Fatalf("old controller received %d requests", oldControllerRequests.Load())
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), newController.URL) || !strings.Contains(string(raw), "new-secret") {
		t.Fatalf("controller settings not switched: %s", raw)
	}
}
