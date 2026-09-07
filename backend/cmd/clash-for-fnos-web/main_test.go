package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
