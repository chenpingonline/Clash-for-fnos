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
