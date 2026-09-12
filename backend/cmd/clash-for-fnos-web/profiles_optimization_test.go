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
	"time"
)

func waitProfileJob(t *testing.T, handler http.Handler, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if response.Code != http.StatusAccepted {
		t.Fatalf("job start status=%d body=%s", response.Code, response.Body.String())
	}
	var job map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	id, _ := job["jobId"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		poll := httptest.NewRecorder()
		handler.ServeHTTP(poll, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/jobs/"+id, nil))
		if poll.Code != http.StatusOK {
			t.Fatalf("job poll status=%d body=%s", poll.Code, poll.Body.String())
		}
		if err := json.Unmarshal(poll.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job["state"] == "done" {
			return job
		}
		if job["state"] == "failed" {
			t.Fatalf("job failed: %#v", job)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job did not finish: %#v", job)
	return nil
}

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
	job := waitProfileJob(t, handler, update)
	encoded, _ := json.Marshal(job["result"])
	if !strings.Contains(string(encoded), `"unchanged":true`) || !strings.Contains(string(encoded), `"conditional":true`) {
		t.Fatalf("update job=%s", encoded)
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
	waitProfileJob(t, handler, update)
	body, err := os.ReadFile(filepath.Join(root, "profiles", id+".yaml"))
	if err != nil || !strings.Contains(string(body), "7892") {
		t.Fatalf("updated profile=%q err=%v", body, err)
	}
}

func TestUpdateActivateProfileDownloadsAndAppliesChangedContent(t *testing.T) {
	t.Parallel()
	var downloads atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		port := int32(7892)
		if downloads.Add(1) == 1 {
			port = 7891
		}
		_, _ = fmt.Fprintf(w, "mixed-port: %d\n", port)
	}))
	defer remote.Close()

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

	socketDir, err := os.MkdirTemp("/tmp", "cff-update-activate-")
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
	helperServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/config/compose":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			writeJSON(w, 200, map[string]any{"content": body["content"].(string) + "ipv6: false\n"})
		case r.Method == http.MethodGet && r.URL.Path == "/config/active-raw":
			writeJSON(w, http.StatusOK, map[string]any{"content": "mixed-port: 7890\n", "path": "/tmp/startup.yaml"})
		case r.Method == http.MethodPost && r.URL.Path == "/config/sync":
			writeJSON(w, http.StatusOK, map[string]any{"txId": "tx-update", "target": "/tmp/startup.yaml", "backup": nil, "validation": map[string]any{"method": "mihomo-test"}})
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
	if err := os.WriteFile(managed, []byte("mixed-port: 7890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newGateway(config{
		publicDir:         root,
		gateway:           "/app/clash-for-fnos",
		profilesFile:      filepath.Join(root, "profiles.json"),
		profileDir:        filepath.Join(root, "profiles"),
		settingsFile:      writeGatewaySettings(t, mihomoServer.URL),
		managedConfigFile: managed,
		configMetaFile:    filepath.Join(root, "config-meta.json"),
		backupDir:         filepath.Join(root, "backups"),
		privilegedSocket:  socketPath,
		selectedFile:      filepath.Join(root, "selected.json"),
	})

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles", strings.NewReader(fmt.Sprintf(`{"Name":"当前订阅","URL":%q}`, remote.URL))))
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
	handler.ServeHTTP(update, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles/"+id+"/update-activate", nil))
	job := waitProfileJob(t, handler, update)
	result, _ := job["result"].(map[string]any)
	if result["unchanged"] != false || applyRequests.Load() != 1 {
		t.Fatalf("job result=%#v applyRequests=%d", result, applyRequests.Load())
	}
	persisted, err := os.ReadFile(managed)
	if err != nil || (!strings.Contains(string(persisted), "mixed-port: 7892") || !strings.Contains(string(persisted), "ipv6: false")) {
		t.Fatalf("managed config=%q err=%v", persisted, err)
	}

	unchangedUpdate := httptest.NewRecorder()
	handler.ServeHTTP(unchangedUpdate, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles/"+id+"/update-activate", nil))
	unchangedJob := waitProfileJob(t, handler, unchangedUpdate)
	unchangedResult, _ := unchangedJob["result"].(map[string]any)
	if unchangedResult["unchanged"] != true || applyRequests.Load() != 1 {
		t.Fatalf("unchanged result=%#v applyRequests=%d", unchangedResult, applyRequests.Load())
	}
}

func TestSyncStartupConfigValidatesBeforeApplyingAndPersists(t *testing.T) {
	t.Parallel()
	oldConfig := []byte("mixed-port: 7890\n")
	newConfig := []byte("mixed-port: 7891\n")
	var applyRequests atomic.Int32
	var validated atomic.Bool
	var appliedBeforeValidation atomic.Bool
	mihomoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer live-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/configs":
			if !validated.Load() {
				appliedBeforeValidation.Store(true)
			}
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
		case r.Method == http.MethodGet && r.URL.Path == "/status":
			writeJSON(w, http.StatusOK, map[string]any{"mode": "managed", "managedController": mihomoServer.URL, "managedSecret": "live-secret"})
		case r.Method == http.MethodGet && r.URL.Path == "/config/active-raw":
			writeJSON(w, http.StatusOK, map[string]any{"content": string(oldConfig), "path": "/tmp/startup.yaml"})
		case r.Method == http.MethodPost && r.URL.Path == "/config/sync":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			skippedValidation.Store(body["skipValidation"] == true)
			validated.Store(body["skipValidation"] != true)
			writeJSON(w, http.StatusOK, map[string]any{"txId": "tx-1", "target": "/tmp/startup.yaml", "backup": nil, "validation": map[string]any{"method": "mihomo-test", "skipped": false}})
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
	progressStages := []string{}
	result, err := gateway.syncStartupConfigWithStage(context.Background(), newConfig, func(stage, _ string) {
		progressStages = append(progressStages, stage)
	})
	if err != nil {
		t.Fatal(err)
	}
	if applyRequests.Load() != 1 {
		t.Fatalf("Mihomo apply requests=%d, want 1", applyRequests.Load())
	}
	if skippedValidation.Load() {
		t.Fatal("helper unexpectedly skipped mihomo -t validation")
	}
	if appliedBeforeValidation.Load() {
		t.Fatal("runtime config was applied before the safe validation completed")
	}
	if got, want := strings.Join(progressStages, ","), "inspect,validate,activate,apply,controller,persist,commit"; got != want {
		t.Fatalf("progress stages=%q, want %q", got, want)
	}
	if result["unchanged"] != false || result["durationMs"] == nil || result["stages"] == nil {
		t.Fatalf("result=%#v", result)
	}
	persisted, err := os.ReadFile(managed)
	if err != nil || !strings.Contains(string(persisted), string(newConfig)) || !strings.Contains(string(persisted), "secret: live-secret") || !strings.Contains(string(persisted), strings.TrimPrefix(mihomoServer.URL, "http://")) {
		t.Fatalf("persisted=%q err=%v", persisted, err)
	}
}
