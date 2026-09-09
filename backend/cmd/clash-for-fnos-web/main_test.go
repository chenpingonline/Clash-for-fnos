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
	"runtime"
	"strings"
	"testing"
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
	handler := newGateway(config{publicDir: root, gateway: "/app/clash-for-fnos"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status = %d", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["backend"] != "go" {
		t.Fatalf("unexpected health payload: %#v", body)
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
	handler := newGateway(config{publicDir: root, gateway: "/app/clash-for-fnos"})
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

func TestProfileImportListPatchAndDeleteAreServedByGo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := config{
		publicDir:    root,
		gateway:      "/app/clash-for-fnos",
		profilesFile: filepath.Join(root, "profiles.json"),
		profileDir:   filepath.Join(root, "profiles"),
	}
	handler := newGateway(cfg)
	importRecorder := httptest.NewRecorder()
	handler.ServeHTTP(importRecorder, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles/import", strings.NewReader(`{"Name":"本地测试","Content":"port: 7890\n"}`)))
	if importRecorder.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", importRecorder.Code, importRecorder.Body.String())
	}
	var imported map[string]any
	if err := json.Unmarshal(importRecorder.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	id, _ := imported["id"].(string)
	if id == "" {
		t.Fatalf("missing imported profile id: %#v", imported)
	}

	patchRecorder := httptest.NewRecorder()
	handler.ServeHTTP(patchRecorder, httptest.NewRequest(http.MethodPatch, "/app/clash-for-fnos/api/profiles/"+id, strings.NewReader(`{"name":"已重命名"}`)))
	if patchRecorder.Code != http.StatusOK || !strings.Contains(patchRecorder.Body.String(), "已重命名") {
		t.Fatalf("patch status=%d body=%s", patchRecorder.Code, patchRecorder.Body.String())
	}

	listRecorder := httptest.NewRecorder()
	handler.ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/profiles", nil))
	if listRecorder.Code != http.StatusOK || !strings.Contains(listRecorder.Body.String(), id) {
		t.Fatalf("list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}

	deleteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(deleteRecorder, httptest.NewRequest(http.MethodDelete, "/app/clash-for-fnos/api/profiles/"+id, nil))
	if deleteRecorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}
}

func TestRemoteProfileDownloadUsesExpectedContract(t *testing.T) {
	t.Parallel()
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != defaultSubscriptionUA {
			t.Fatalf("unexpected user agent: %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("subscription-userinfo", "upload=10; download=20; total=100; expire=123")
		w.Header().Set("profile-web-page-url", "https://example.test/account")
		_, _ = io.WriteString(w, "port: 7890\n")
	}))
	defer remote.Close()
	root := t.TempDir()
	handler := newGateway(config{publicDir: root, gateway: "/app/clash-for-fnos", profilesFile: filepath.Join(root, "profiles.json"), profileDir: filepath.Join(root, "profiles")})
	recorder := httptest.NewRecorder()
	payload := fmt.Sprintf(`{"Name":"远程订阅","URL":%q}`, remote.URL)
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles", strings.NewReader(payload)))
	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"total":100`) || !strings.Contains(recorder.Body.String(), `"label":"直连"`) {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAuthorizedLocalConfigDiscoveryAndImportAreServedByGo(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.MkdirAll(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(shared, "config.yaml")
	if err := os.WriteFile(configFile, []byte("mixed-port: 7890\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	authorizedFile := filepath.Join(root, "authorized-paths.txt")
	if err := os.WriteFile(authorizedFile, []byte(shared), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newGateway(config{
		publicDir:         root,
		gateway:           "/app/clash-for-fnos",
		profilesFile:      filepath.Join(root, "profiles.json"),
		profileDir:        filepath.Join(root, "profiles"),
		managedConfigFile: filepath.Join(root, "managed.yaml"),
		configMetaFile:    filepath.Join(root, "config-meta.json"),
		backupDir:         filepath.Join(root, "backups"),
		authorizedFile:    authorizedFile,
	})
	discoverRecorder := httptest.NewRecorder()
	handler.ServeHTTP(discoverRecorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/local-config/discover", nil))
	if discoverRecorder.Code != http.StatusOK {
		t.Fatalf("discover status=%d body=%s", discoverRecorder.Code, discoverRecorder.Body.String())
	}
	var discovery struct {
		Candidates []localCandidate `json:"candidates"`
	}
	if err := json.Unmarshal(discoverRecorder.Body.Bytes(), &discovery); err != nil {
		t.Fatal(err)
	}
	if len(discovery.Candidates) != 1 || discovery.Candidates[0].Token == "" {
		t.Fatalf("unexpected discovery: %#v", discovery)
	}
	importRecorder := httptest.NewRecorder()
	payload := fmt.Sprintf(`{"Token":%q,"Apply":false}`, discovery.Candidates[0].Token)
	handler.ServeHTTP(importRecorder, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/local-config/import", strings.NewReader(payload)))
	if importRecorder.Code != http.StatusOK || !strings.Contains(importRecorder.Body.String(), configFile) {
		t.Fatalf("import status=%d body=%s", importRecorder.Code, importRecorder.Body.String())
	}
}

func TestSystemFacadeCallsPrivilegedHelperDirectly(t *testing.T) {
	t.Parallel()
	socketDir, err := os.MkdirTemp("/tmp", "cff-helper-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "helper.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/app/icon/status" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"selected": "neon-cat", "items": []any{}})
	})}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })

	root := t.TempDir()
	handler := newGateway(config{publicDir: root, gateway: "/app/clash-for-fnos", privilegedSocket: socketPath})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/app/icons", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"selected":"neon-cat"`) {
		t.Fatalf("icons status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestVersionAndMihomoAssetSelection(t *testing.T) {
	t.Parallel()
	if compareVersion("v1.20.0", "v1.19.9") <= 0 || compareVersion("0.8.16", "v0.8.16") != 0 {
		t.Fatal("semantic version comparison failed")
	}
	release := githubRelease{TagName: "v1.20.0"}
	assetName := "mihomo-linux-" + runtime.GOARCH + "-v1.20.0.gz"
	if runtime.GOARCH == "amd64" {
		assetName = "mihomo-linux-amd64-v2-v1.20.0.gz"
	}
	release.Assets = []githubAsset{{Name: assetName, BrowserDownloadURL: "https://example.test/mihomo.gz", Digest: "sha256:abc"}}
	asset, err := selectMihomoAsset(release)
	if err != nil || asset["name"] != assetName || asset["sha256"] != "abc" {
		t.Fatalf("asset=%#v err=%v", asset, err)
	}
}

func TestControllerRoutesAreServedByGo(t *testing.T) {
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
		settingsFile: writeGatewaySettings(t, controller.URL),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/stream/traffic", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "data: {\"up\":1}\n\ndata: {\"down\":2}\n\n" {
		t.Fatalf("traffic response: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestMemoryStreamIsConvertedToSSEByGo(t *testing.T) {
	t.Parallel()
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/memory" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "{\"inuse\":0,\"oslimit\":0}\n{\"inuse\":66270003,\"oslimit\":0}\n")
	}))
	defer controller.Close()
	handler := newGateway(config{
		publicDir:    t.TempDir(),
		gateway:      "/app/clash-for-fnos",
		settingsFile: writeGatewaySettings(t, controller.URL),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/stream/memory", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "data: {\"inuse\":0,\"oslimit\":0}\n\ndata: {\"inuse\":66270003,\"oslimit\":0}\n\n" {
		t.Fatalf("memory response: status=%d body=%q", recorder.Code, recorder.Body.String())
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
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", mihomoLogFile: logFile})
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

func TestManagerSettingsArePersistedByGo(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	settingsFile := filepath.Join(directory, "settings.json")
	if err := os.WriteFile(settingsFile, []byte(`{"dnsOverrideEnabled":true,"secret":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", settingsFile: settingsFile, privilegedSocket: filepath.Join(directory, "missing-helper.sock")})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/settings", strings.NewReader(`{"controller":"http://127.0.0.1:9191/","persistSelections":false,"clearSecret":true}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"controller":"http://127.0.0.1:9191"`) {
		t.Fatalf("update response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body, err := os.ReadFile(settingsFile)
	if err != nil || !strings.Contains(string(body), `"dnsOverrideEnabled": true`) {
		t.Fatalf("unrelated settings were not preserved: %s err=%v", body, err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/settings", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"persistSelections":false`) {
		t.Fatalf("get response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestStartupRestoresOnlyValidSavedSelections(t *testing.T) {
	t.Parallel()
	selected := make(chan string, 1)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/proxies" {
			_, _ = io.WriteString(w, `{"proxies":{"Group":{"all":["A","B"]}}}`)
			return
		}
		if r.Method == http.MethodPut && r.URL.Path == "/proxies/Group" {
			body, _ := io.ReadAll(r.Body)
			selected <- string(body)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer controller.Close()
	directory := t.TempDir()
	settingsFile := filepath.Join(directory, "settings.json")
	body, _ := json.Marshal(map[string]any{"controller": controller.URL, "persistSelections": true})
	_ = os.WriteFile(settingsFile, body, 0o600)
	selectedFile := filepath.Join(directory, "selected.json")
	_ = os.WriteFile(selectedFile, []byte(`{"Group":"B","Missing":"A"}`), 0o600)
	handler := newGateway(config{settingsFile: settingsFile, selectedFile: selectedFile, mihomoLogFile: filepath.Join(directory, "log")})
	handler.restoreSelections(context.Background())
	select {
	case value := <-selected:
		if value != `{"name":"B"}` {
			t.Fatalf("unexpected body: %s", value)
		}
	default:
		t.Fatal("saved selection was not restored")
	}
}

func TestConfigReadEndpointsAreServedByGo(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	managed := filepath.Join(directory, "config.yaml")
	meta := filepath.Join(directory, "config-meta.json")
	backups := filepath.Join(directory, "backups")
	_ = os.Mkdir(backups, 0o700)
	_ = os.WriteFile(managed, []byte("mode: rule\n"), 0o600)
	_ = os.WriteFile(meta, []byte(`{"source":"profile"}`), 0o600)
	_ = os.WriteFile(filepath.Join(backups, "2026-01.yaml"), []byte("x"), 0o600)
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", managedConfigFile: managed, configMetaFile: meta, backupDir: backups, mihomoLogFile: filepath.Join(directory, "log")})
	for path, part := range map[string]string{"/api/config/raw": "mode: rule", "/api/config/meta": `"source":"profile"`, "/api/config/backups": "2026-01.yaml"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos"+path, nil))
		if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), part) {
			t.Fatalf("%s: status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRawConfigSaveAppliesBacksUpAndUpdatesMetadata(t *testing.T) {
	t.Parallel()
	applied := make(chan string, 1)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/configs" || r.URL.Query().Get("force") != "true" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		applied <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer controller.Close()
	directory := t.TempDir()
	settings := filepath.Join(directory, "settings.json")
	body, _ := json.Marshal(map[string]any{"controller": controller.URL})
	_ = os.WriteFile(settings, body, 0o600)
	managed := filepath.Join(directory, "config.yaml")
	_ = os.WriteFile(managed, []byte("mode: rule\n"), 0o600)
	handler := newGateway(config{publicDir: t.TempDir(), gateway: "/app/clash-for-fnos", settingsFile: settings, managedConfigFile: managed, configMetaFile: filepath.Join(directory, "config-meta.json"), backupDir: filepath.Join(directory, "backups"), mihomoLogFile: filepath.Join(directory, "log")})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/config/raw", strings.NewReader("mode: direct\n")))
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if value := <-applied; !strings.Contains(value, `"payload":"mode: direct\n"`) {
		t.Fatalf("unexpected apply body: %s", value)
	}
	saved, _ := os.ReadFile(managed)
	if string(saved) != "mode: direct\n" {
		t.Fatalf("unexpected config: %s", saved)
	}
	entries, _ := os.ReadDir(filepath.Join(directory, "backups"))
	if len(entries) != 1 {
		t.Fatalf("expected backup, got %d", len(entries))
	}
}
