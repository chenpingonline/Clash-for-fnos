package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProfileUpdateUsesConditionalRequestAndSkipsUnchangedContent(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber := requests.Add(1)
		if requestNumber > 1 {
			if got := r.Header.Get("If-None-Match"); got != `"profile-v1"` {
				t.Errorf("If-None-Match=%q", got)
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"profile-v1"`)
		w.Header().Set("subscription-userinfo", "upload=10; download=20; total=100")
		_, _ = io.WriteString(w, "mixed-port: 7890\n")
	}))
	defer remote.Close()

	root := t.TempDir()
	cfg := config{publicDir: root, gateway: "/app/clash-for-fnos", profilesFile: filepath.Join(root, "profiles.json"), profileDir: filepath.Join(root, "profiles")}
	handler := newGateway(cfg)
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles", strings.NewReader(fmt.Sprintf(`{"Name":"条件更新","URL":%q}`, remote.URL))))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	update := httptest.NewRecorder()
	handler.ServeHTTP(update, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles/"+id+"/update", nil))
	if update.Code != http.StatusOK || !strings.Contains(update.Body.String(), `"unchanged":true`) || !strings.Contains(update.Body.String(), `"conditional":true`) {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}
}

func TestManualProfileUpdateDoesNotImplicitlyApply(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		port := 7890 + requests.Add(1)
		_, _ = fmt.Fprintf(w, "mixed-port: %d\n", port)
	}))
	defer remote.Close()

	root := t.TempDir()
	cfg := config{publicDir: root, gateway: "/app/clash-for-fnos", profilesFile: filepath.Join(root, "profiles.json"), profileDir: filepath.Join(root, "profiles")}
	handler := newGateway(cfg)
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles", strings.NewReader(fmt.Sprintf(`{"Name":"仅更新","URL":%q,"autoApply":true}`, remote.URL))))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)
	state, err := handler.readProfiles()
	if err != nil {
		t.Fatal(err)
	}
	state.Current = &id
	if err := handler.writeProfiles(state); err != nil {
		t.Fatal(err)
	}

	update := httptest.NewRecorder()
	handler.ServeHTTP(update, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles/"+id+"/update", nil))
	if update.Code != http.StatusOK {
		t.Fatalf("manual update unexpectedly attempted apply: status=%d body=%s", update.Code, update.Body.String())
	}
	body, err := os.ReadFile(filepath.Join(root, "profiles", id+".yaml"))
	if err != nil || !strings.Contains(string(body), "7892") {
		t.Fatalf("updated profile=%q err=%v", body, err)
	}
}

func TestSyncStartupConfigParsesOnceThenPersistsWithoutMihomoTest(t *testing.T) {
	t.Parallel()
	oldConfig := []byte("mixed-port: 7890\n")
	newConfig := []byte("mixed-port: 7891\n")
	var applyRequests atomic.Int32
	mihomoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/configs":
			applyRequests.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/version":
			_, _ = io.WriteString(w, `{"version":"test"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mihomoServer.Close()

	socketDir, err := os.MkdirTemp("/tmp", "cff-config-opt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "helper.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var skippedValidation atomic.Bool
	helperServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/config/active-raw":
			writeJSON(w, http.StatusOK, map[string]any{"content": string(oldConfig), "path": "/tmp/startup.yaml"})
		case r.Method == http.MethodPost && r.URL.Path == "/config/sync":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			skippedValidation.Store(body["skipValidation"] == true)
			writeJSON(w, http.StatusOK, map[string]any{"txId": "tx-1", "target": "/tmp/startup.yaml", "backup": nil, "validation": map[string]any{"method": "live-apply", "skipped": true}})
		case r.Method == http.MethodPost && (r.URL.Path == "/config/activate" || r.URL.Path == "/config/commit"):
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "method": "hot-reload"})
		default:
			http.NotFound(w, r)
		}
	})}
	go helperServer.Serve(listener)
	defer helperServer.Close()

	root := t.TempDir()
	managed := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(managed, oldConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	gateway := newGateway(config{
		settingsFile:      writeGatewaySettings(t, mihomoServer.URL),
		managedConfigFile: managed,
		configMetaFile:    filepath.Join(root, "config-meta.json"),
		backupDir:         filepath.Join(root, "backups"),
		privilegedSocket:  socketPath,
		selectedFile:      filepath.Join(root, "selected.json"),
	})
	result, err := gateway.syncStartupConfig(context.Background(), newConfig)
	if err != nil {
		t.Fatal(err)
	}
	if applyRequests.Load() != 1 {
		t.Fatalf("Mihomo apply requests=%d, want 1", applyRequests.Load())
	}
	if !skippedValidation.Load() {
		t.Fatal("helper did not receive skipValidation after successful live apply")
	}
	if result["unchanged"] != false || result["durationMs"] == nil || result["stages"] == nil {
		t.Fatalf("result=%#v", result)
	}
	persisted, err := os.ReadFile(managed)
	if err != nil || string(persisted) != string(newConfig) {
		t.Fatalf("persisted=%q err=%v", persisted, err)
	}
}
