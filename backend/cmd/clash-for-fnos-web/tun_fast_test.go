package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startTunHelper(t *testing.T, handler http.Handler) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "cff-tun-helper-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socketPath := filepath.Join(directory, "helper.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return socketPath
}

func TestTunFastPathPatchesRuntimeThenPersists(t *testing.T) {
	t.Parallel()
	helperRequests := []string{}
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		helperRequests = append(helperRequests, r.URL.Path)
		switch r.URL.Path {
		case "/network/tun":
			writeJSON(w, http.StatusOK, map[string]any{"txId": "tun-tx", "previousEnabled": false})
		case "/config/validate", "/config/activate", "/config/commit":
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))

	runtimeEnabled := false
	patchBody := ""
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			body, _ := io.ReadAll(r.Body)
			patchBody = string(body)
			var payload struct {
				Tun struct {
					Enable bool `json:"enable"`
				} `json:"tun"`
			}
			_ = json.Unmarshal(body, &payload)
			runtimeEnabled = payload.Tun.Enable
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/configs":
			writeJSON(w, http.StatusOK, map[string]any{"tun": map[string]bool{"enable": runtimeEnabled}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	handler := newGateway(config{
		publicDir: t.TempDir(), gateway: "/app/clash-for-fnos",
		settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: helperSocket,
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/network/tun", strings.NewReader(`{"enabled":true}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"activation":"patch"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if patchBody != `{"tun":{"enable":true}}` {
		t.Fatalf("unexpected PATCH body: %s", patchBody)
	}
	if strings.Join(helperRequests, ",") != "/network/tun,/config/validate,/config/activate,/config/commit" {
		t.Fatalf("unexpected helper sequence: %v", helperRequests)
	}
}

func TestStartupReconcilesEnabledManagedTun(t *testing.T) {
	t.Parallel()
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			writeJSON(w, http.StatusOK, map[string]any{"mode": "managed"})
		case "/network/status":
			writeJSON(w, http.StatusOK, map[string]any{"settings": map[string]any{"tun": map[string]any{"enabled": true}}})
		default:
			http.NotFound(w, r)
		}
	}))

	runtimeEnabled := true
	patches := []bool{}
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/version":
			writeJSON(w, http.StatusOK, map[string]any{"version": "test"})
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			var payload struct {
				Tun struct {
					Enable bool `json:"enable"`
				} `json:"tun"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			runtimeEnabled = payload.Tun.Enable
			patches = append(patches, runtimeEnabled)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/configs":
			writeJSON(w, http.StatusOK, map[string]any{"tun": map[string]bool{"enable": runtimeEnabled}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	gateway := newGateway(config{settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: helperSocket})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gateway.reconcileManagedTunAfterStartup(ctx)
	if len(patches) != 2 || patches[0] || !patches[1] || !runtimeEnabled {
		t.Fatalf("patches=%v runtimeEnabled=%t", patches, runtimeEnabled)
	}
}

func TestStartupDoesNotTouchDisabledManagedTun(t *testing.T) {
	t.Parallel()
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			writeJSON(w, http.StatusOK, map[string]any{"mode": "managed"})
		case "/network/status":
			writeJSON(w, http.StatusOK, map[string]any{"settings": map[string]any{"tun": map[string]any{"enabled": false}}})
		default:
			http.NotFound(w, r)
		}
	}))

	patches := 0
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/version" {
			writeJSON(w, http.StatusOK, map[string]any{"version": "test"})
			return
		}
		if r.Method == http.MethodPatch && r.URL.Path == "/configs" {
			patches++
		}
		http.NotFound(w, r)
	}))
	defer controller.Close()

	gateway := newGateway(config{settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: helperSocket})
	gateway.reconcileManagedTunAfterStartup(context.Background())
	if patches != 0 {
		t.Fatalf("unexpected startup TUN patches: %d", patches)
	}
}

func TestStartupFallsBackToManagedRestartWhenTunReenableFails(t *testing.T) {
	t.Parallel()
	runtimeEnabled := true
	restarts := 0
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			writeJSON(w, http.StatusOK, map[string]any{"mode": "managed"})
		case "/network/status":
			writeJSON(w, http.StatusOK, map[string]any{"settings": map[string]any{"tun": map[string]any{"enabled": true}}})
		case "/core/restart-managed":
			restarts++
			runtimeEnabled = true
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "managed"})
		default:
			http.NotFound(w, r)
		}
	}))

	patches := []bool{}
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/version":
			writeJSON(w, http.StatusOK, map[string]any{"version": "test"})
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			var payload struct {
				Tun struct {
					Enable bool `json:"enable"`
				} `json:"tun"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			patches = append(patches, payload.Tun.Enable)
			if payload.Tun.Enable {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "route already exists"})
				return
			}
			runtimeEnabled = false
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/configs":
			writeJSON(w, http.StatusOK, map[string]any{"tun": map[string]bool{"enable": runtimeEnabled}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	gateway := newGateway(config{settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: helperSocket})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gateway.reconcileManagedTunAfterStartup(ctx)
	if len(patches) != 2 || patches[0] || !patches[1] || restarts != 1 || !runtimeEnabled {
		t.Fatalf("patches=%v restarts=%d runtimeEnabled=%t", patches, restarts, runtimeEnabled)
	}
}

func TestTunFastPathRollsBackAfterRuntimeFailure(t *testing.T) {
	t.Parallel()
	helperRequests := []string{}
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		helperRequests = append(helperRequests, r.URL.Path)
		if r.URL.Path == "/network/tun" {
			writeJSON(w, http.StatusOK, map[string]any{"txId": "tun-tx", "previousEnabled": false})
			return
		}
		if r.URL.Path == "/config/validate" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "method": "mihomo-test"})
			return
		}
		if r.URL.Path == "/config/rollback" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		http.NotFound(w, r)
	}))

	patches := 0
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			patches++
			if patches == 1 {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "tun failed"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/configs":
			writeJSON(w, http.StatusOK, map[string]any{"tun": map[string]bool{"enable": false}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	handler := newGateway(config{
		publicDir: t.TempDir(), gateway: "/app/clash-for-fnos",
		settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: helperSocket,
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/network/tun", strings.NewReader(`{"enabled":true}`)))
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "已回滚") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if patches != 2 || strings.Join(helperRequests, ",") != "/network/tun,/config/validate,/status,/config/rollback" {
		t.Fatalf("patches=%d helper=%v", patches, helperRequests)
	}
}

func TestTunDoesNotTouchRuntimeWhenValidationFails(t *testing.T) {
	t.Parallel()
	helperRequests := []string{}
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		helperRequests = append(helperRequests, r.URL.Path)
		switch r.URL.Path {
		case "/network/tun":
			writeJSON(w, http.StatusOK, map[string]any{"txId": "tun-tx", "previousEnabled": false})
		case "/config/validate":
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Mihomo 配置校验失败"})
		case "/config/rollback":
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))

	patches := 0
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && r.URL.Path == "/configs" {
			patches++
		}
		http.NotFound(w, r)
	}))
	defer controller.Close()

	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: helperSocket})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/network/tun", strings.NewReader(`{"enabled":true}`)))
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "未修改运行状态") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if patches != 0 || strings.Join(helperRequests, ",") != "/network/tun,/config/validate,/config/rollback" {
		t.Fatalf("patches=%d helper=%v", patches, helperRequests)
	}
}

func TestTunFastPathPollsAfterDelayedInterfaceRelease(t *testing.T) {
	t.Parallel()
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/network/tun":
			writeJSON(w, http.StatusOK, map[string]any{"txId": "tun-tx", "previousEnabled": false, "effectiveContent": "tun:\n  enable: true\n"})
		case "/config/validate", "/config/activate", "/config/commit":
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))

	patches := 0
	reads := 0
	runtimeEnabled := false
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			patches++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/configs":
			reads++
			if reads >= 2 {
				runtimeEnabled = true
			}
			writeJSON(w, http.StatusOK, map[string]any{"tun": map[string]bool{"enable": runtimeEnabled}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: helperSocket})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/network/tun", strings.NewReader(`{"enabled":true}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"activation":"patch"`) || patches != 1 || reads != 2 {
		t.Fatalf("status=%d patches=%d reads=%d body=%s", recorder.Code, patches, reads, recorder.Body.String())
	}
}

func TestTunFastPathRestartsManagedCoreAfterRuntimeFailure(t *testing.T) {
	t.Parallel()
	helperRequests := []string{}
	runtimeEnabled := false
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		helperRequests = append(helperRequests, r.URL.Path)
		switch r.URL.Path {
		case "/network/tun":
			writeJSON(w, http.StatusOK, map[string]any{"txId": "tun-tx", "previousEnabled": false, "effectiveContent": "tun:\n  enable: true\n"})
		case "/status":
			writeJSON(w, http.StatusOK, map[string]any{"mode": "managed", "canRestartService": true})
		case "/core/restart-managed":
			runtimeEnabled = true
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "managed"})
		case "/config/validate", "/config/activate", "/config/commit":
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))

	patches := 0
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			patches++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/version":
			writeJSON(w, http.StatusOK, map[string]any{"version": "test"})
		case r.Method == http.MethodGet && r.URL.Path == "/configs":
			writeJSON(w, http.StatusOK, map[string]any{"tun": map[string]bool{"enable": runtimeEnabled}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", settingsFile: writeGatewaySettings(t, controller.URL), privilegedSocket: helperSocket})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/network/tun", strings.NewReader(`{"enabled":true}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"activation":"managed-restart"`) || patches != 1 {
		t.Fatalf("status=%d patches=%d helper=%v body=%s", recorder.Code, patches, helperRequests, recorder.Body.String())
	}
	wantHelpers := "/network/tun,/config/validate,/status,/config/activate,/core/restart-managed,/status,/config/commit"
	if strings.Join(helperRequests, ",") != wantHelpers {
		t.Fatalf("helper=%v want=%s", helperRequests, wantHelpers)
	}
}

func TestRecentTunErrorReturnsLatestMihomoFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "mihomo.log")
	content := "level=info msg=ready\nlevel=error msg=Start TUN listening error: route already exists\n"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := recentTunError(file); !strings.Contains(got, "route already exists") {
		t.Fatalf("recentTunError=%q", got)
	}
}

func TestTunOperationStatusEndpointReportsCurrentStage(t *testing.T) {
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos"})
	handler.setTunOperation(true, true, "restart-managed")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/network/tun/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var status tunOperationStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Active || !status.Enabled || status.Stage != "restart-managed" || !strings.Contains(status.Message, "重启 Core") || status.StartedAt == 0 {
		t.Fatalf("unexpected status: %#v", status)
	}
}
