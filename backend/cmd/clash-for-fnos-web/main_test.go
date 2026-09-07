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

func writeGatewaySettings(t *testing.T, controller string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "settings.json")
	body, err := json.Marshal(map[string]any{"controller": controller, "secret": "gateway-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestStripPrefix(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/app/clash-for-fnos":              "/",
		"/app/clash-for-fnos/":             "/",
		"/app/clash-for-fnos/api/settings": "/api/settings",
		"/api/settings":                    "/api/settings",
	}
	for input, expected := range cases {
		if actual := stripPrefix(input, "/app/clash-for-fnos"); actual != expected {
			t.Fatalf("stripPrefix(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestValidStaticPath(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"index.html", "assets/index.js", "icons/cat.png"} {
		if !validStaticPath(value) {
			t.Fatalf("expected %q to be valid", value)
		}
	}
	for _, value := range []string{"", "../secret", "assets/../../secret", `assets\secret`} {
		if validStaticPath(value) {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestHealthIsServedByGo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("app"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newGateway(config{publicDir: root, gateway: "/app/clash-for-fnos", upstreamPath: filepath.Join(root, "missing.sock")})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status = %d", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["backend"] != "go" || body["compatibilityBackend"] != "node" {
		t.Fatalf("unexpected health payload: %#v", body)
	}
}

func TestAPIIsProxiedThroughUnixSocket(t *testing.T) {
	t.Parallel()
	socketDir, err := os.MkdirTemp("/tmp", "cff-go-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "legacy.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"path": r.URL.Path})
	})}
	go server.Serve(listener)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})

	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", upstreamPath: socketPath})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/settings", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("proxy status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["path"] != "/app/clash-for-fnos/api/settings" {
		t.Fatalf("proxied path = %q", body["path"])
	}
}

func TestStaticFilesAndSPAFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("app shell"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app.js"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newGateway(config{publicDir: root, gateway: "/app/clash-for-fnos", upstreamPath: filepath.Join(root, "missing.sock")})
	for requestPath, expected := range map[string]string{
		"/app/clash-for-fnos/assets/app.js": "asset",
		"/app/clash-for-fnos/settings":      "app shell",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if recorder.Code != http.StatusOK || recorder.Body.String() != expected {
			t.Fatalf("GET %s: status=%d body=%q", requestPath, recorder.Code, recorder.Body.String())
		}
	}
}

func TestMigratedControllerRoutesBypassNodeCompatibilityService(t *testing.T) {
	t.Parallel()
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gateway-secret" {
			t.Fatalf("missing controller authorization")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/connections":
			_, _ = io.WriteString(w, `{"connections":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/version":
			_, _ = io.WriteString(w, `{"version":"1.2.3"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/configs":
			_, _ = io.WriteString(w, `{"mode":"rule"}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"mode":"direct"}` {
				t.Fatalf("unexpected patch body: %s", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/proxies/node/delay":
			if r.URL.Query().Get("url") != "https://www.gstatic.com/generate_204" || r.URL.Query().Get("timeout") != "5000" {
				t.Fatalf("unexpected delay query: %s", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"delay":12}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()
	handler := newGateway(config{
		publicDir:    t.TempDir(),
		gateway:      "/app/clash-for-fnos",
		upstreamPath: filepath.Join(t.TempDir(), "missing-node.sock"),
		settingsFile: writeGatewaySettings(t, controller.URL),
	})

	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/connections", nil))
	if getRecorder.Code != http.StatusOK || getRecorder.Body.String() != `{"connections":[]}` {
		t.Fatalf("connections response: status=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}

	patchRecorder := httptest.NewRecorder()
	handler.ServeHTTP(patchRecorder, httptest.NewRequest(http.MethodPatch, "/app/clash-for-fnos/api/runtime-config", io.NopCloser(strings.NewReader(`{"mode":"direct"}`))))
	if patchRecorder.Code != http.StatusOK || patchRecorder.Body.String() != `{"ok":true}` {
		t.Fatalf("runtime patch response: status=%d body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}

	delayRecorder := httptest.NewRecorder()
	handler.ServeHTTP(delayRecorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/delay/node", nil))
	if delayRecorder.Code != http.StatusOK || delayRecorder.Body.String() != `{"delay":12}` {
		t.Fatalf("delay response: status=%d body=%s", delayRecorder.Code, delayRecorder.Body.String())
	}

	statusRecorder := httptest.NewRecorder()
	handler.ServeHTTP(statusRecorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/status", nil))
	if statusRecorder.Code != http.StatusOK || !strings.Contains(statusRecorder.Body.String(), `"online":true`) {
		t.Fatalf("status response: status=%d body=%s", statusRecorder.Code, statusRecorder.Body.String())
	}

	testRecorder := httptest.NewRecorder()
	handler.ServeHTTP(testRecorder, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/settings/test", nil))
	if testRecorder.Code != http.StatusOK || !strings.Contains(testRecorder.Body.String(), `"version":"1.2.3"`) {
		t.Fatalf("settings test response: status=%d body=%s", testRecorder.Code, testRecorder.Body.String())
	}
}

func TestTrafficStreamIsConvertedToSSEByGo(t *testing.T) {
	t.Parallel()
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/traffic" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "{\"up\":1}\n{\"down\":2}\n")
	}))
	defer controller.Close()
	handler := newGateway(config{
		publicDir:    t.TempDir(),
		gateway:      "/app/clash-for-fnos",
		upstreamPath: filepath.Join(t.TempDir(), "missing-node.sock"),
		settingsFile: writeGatewaySettings(t, controller.URL),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/stream/traffic", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "data: {\"up\":1}\n\ndata: {\"down\":2}\n\n" {
		t.Fatalf("traffic response: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestProxySelectionIsPersistedByGo(t *testing.T) {
	t.Parallel()
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/proxies/Auto Select" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.EscapedPath())
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"name":"Hong Kong"}` {
			t.Fatalf("unexpected selection body: %s", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer controller.Close()
	directory := t.TempDir()
	settingsFile := filepath.Join(directory, "settings.json")
	settingsBody, _ := json.Marshal(map[string]any{"controller": controller.URL, "persistSelections": true})
	if err := os.WriteFile(settingsFile, settingsBody, 0o600); err != nil {
		t.Fatal(err)
	}
	selectedFile := filepath.Join(directory, "selected.json")
	handler := newGateway(config{
		publicDir: t.TempDir(), gateway: "/app/clash-for-fnos",
		upstreamPath: filepath.Join(t.TempDir(), "missing-node.sock"),
		settingsFile: settingsFile, selectedFile: selectedFile,
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/proxies/Auto%20Select", strings.NewReader(`{"name":"Hong Kong"}`)))
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"ok":true}` {
		t.Fatalf("selection response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var saved map[string]string
	body, err := os.ReadFile(selectedFile)
	if err != nil || json.Unmarshal(body, &saved) != nil || saved["Auto Select"] != "Hong Kong" {
		t.Fatalf("unexpected saved selection: body=%s err=%v", body, err)
	}
}

func TestProxyGroupsUseManagedConfigOrderWhenHelperIsUnavailable(t *testing.T) {
	t.Parallel()
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/proxies" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"proxies":{"B":{"type":"Selector"},"A":{"type":"Selector"}}}`)
	}))
	defer controller.Close()
	directory := t.TempDir()
	managedConfig := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(managedConfig, []byte("proxy-groups:\n  - name: A\n  - name: B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newGateway(config{
		publicDir: t.TempDir(), gateway: "/app/clash-for-fnos",
		upstreamPath:      filepath.Join(t.TempDir(), "missing-node.sock"),
		settingsFile:      writeGatewaySettings(t, controller.URL),
		managedConfigFile: managedConfig,
		privilegedSocket:  filepath.Join(directory, "missing-helper.sock"),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/proxies", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		GroupOrder []string `json:"groupOrder"`
		Source     string   `json:"groupOrderSource"`
		Path       string   `json:"groupOrderPath"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Join(payload.GroupOrder, ",") != "A,B" || payload.Source != "managed" || payload.Path != managedConfig {
		t.Fatalf("unexpected ordered proxies metadata: %#v", payload)
	}
}

func TestRuleProviderUpdateUsesDirectFallbackAndRestoresMode(t *testing.T) {
	t.Parallel()
	requests := make([]string, 0, 5)
	providerAttempts := 0
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.EscapedPath()+" "+string(body))
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/providers/rules/Geo Site":
			providerAttempts++
			if providerAttempts == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"message":"network unavailable"}`)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/configs":
			_, _ = io.WriteString(w, `{"mode":"rule"}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()
	handler := newGateway(config{
		publicDir: t.TempDir(), gateway: "/app/clash-for-fnos",
		upstreamPath: filepath.Join(t.TempDir(), "missing-node.sock"),
		settingsFile: writeGatewaySettings(t, controller.URL),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/rule-providers/Geo%20Site/update", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"method":"direct-fallback"`) {
		t.Fatalf("fallback response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	expected := []string{
		"PUT /providers/rules/Geo%20Site ",
		"GET /configs ",
		`PATCH /configs {"mode":"direct"}`,
		"PUT /providers/rules/Geo%20Site ",
		`PATCH /configs {"mode":"rule"}`,
	}
	if strings.Join(requests, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("unexpected fallback sequence:\n%s", strings.Join(requests, "\n"))
	}
}

func TestLogHistoryIsServedAndClearedByGo(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	logFile := filepath.Join(directory, "mihomo.log")
	if err := os.WriteFile(logFile, []byte("{\"time\":\"now\",\"level\":\"warning\",\"message\":\"warn\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", upstreamPath: filepath.Join(directory, "missing.sock"), mihomoLogFile: logFile})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/logs/history?level=warning&limit=10", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"message":"warn"`) {
		t.Fatalf("history response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/app/clash-for-fnos/api/logs/history", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != `{"ok":true}` {
		t.Fatalf("clear response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
