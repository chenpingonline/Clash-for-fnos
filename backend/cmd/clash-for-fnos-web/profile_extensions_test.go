package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProfileExtensionAPIUsesIndependentValidatedFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	handler := newGateway(config{gateway: "/app/clash-for-fnos", profilesFile: filepath.Join(root, "profiles.json"), profileDir: filepath.Join(root, "profiles")})
	imported := httptest.NewRecorder()
	handler.ServeHTTP(imported, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles/import", strings.NewReader(`{"Name":"本地测试","Content":"rules: [MATCH,DIRECT]\n"}`)))
	if imported.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", imported.Code, imported.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(imported.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	id := item["id"].(string)

	getDefault := httptest.NewRecorder()
	handler.ServeHTTP(getDefault, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/profiles/"+id+"/extensions/rules", nil))
	if getDefault.Code != http.StatusOK || !strings.Contains(getDefault.Body.String(), `"customized":false`) || !strings.Contains(getDefault.Body.String(), "prepend") {
		t.Fatalf("default status=%d body=%s", getDefault.Code, getDefault.Body.String())
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/profiles/"+id+"/extensions/rules", strings.NewReader(`{"content":"prepend: ["}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}

	content := "prepend:\n  - DOMAIN-SUFFIX,example.com,DIRECT\nappend: []\ndelete: []\n"
	saved := httptest.NewRecorder()
	handler.ServeHTTP(saved, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/profiles/"+id+"/extensions/rules", strings.NewReader(`{"content":`+mustJSON(content)+`}`)))
	if saved.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body.String())
	}
	body, err := os.ReadFile(filepath.Join(root, "profiles", id+".rules.yaml"))
	if err != nil || string(body) != content {
		t.Fatalf("saved content=%q err=%v", body, err)
	}
	original, err := os.ReadFile(filepath.Join(root, "profiles", id+".yaml"))
	if err != nil || strings.Contains(string(original), "example.com") {
		t.Fatalf("original profile was modified: %q err=%v", original, err)
	}

	reset := httptest.NewRecorder()
	handler.ServeHTTP(reset, httptest.NewRequest(http.MethodDelete, "/app/clash-for-fnos/api/profiles/"+id+"/extensions/rules", nil))
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", reset.Code, reset.Body.String())
	}
	if _, err = os.Stat(filepath.Join(root, "profiles", id+".rules.yaml")); !os.IsNotExist(err) {
		t.Fatalf("extension still exists after reset: %v", err)
	}

	independent := map[string]struct {
		content string
		file    string
	}{
		"override": {content: "mode: global\n", file: id + ".override.yaml"},
		"script":   {content: "function main(config, profileName) { config.profileName = profileName; return config; }\n", file: id + ".script.js"},
	}
	for kind, expected := range independent {
		endpoint := "/app/clash-for-fnos/api/profiles/" + id + "/extensions/" + kind
		saved = httptest.NewRecorder()
		handler.ServeHTTP(saved, httptest.NewRequest(http.MethodPut, endpoint, strings.NewReader(`{"content":`+mustJSON(expected.content)+`}`)))
		if saved.Code != http.StatusOK {
			t.Fatalf("save %s status=%d body=%s", kind, saved.Code, saved.Body.String())
		}
		body, err = os.ReadFile(filepath.Join(root, "profiles", expected.file))
		if err != nil || string(body) != expected.content {
			t.Fatalf("saved %s content=%q err=%v", kind, body, err)
		}
		read := httptest.NewRecorder()
		handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, endpoint, nil))
		var response struct {
			Content    string `json:"content"`
			Customized bool   `json:"customized"`
		}
		if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &response) != nil || !response.Customized || response.Content != expected.content {
			t.Fatalf("read %s status=%d body=%s", kind, read.Code, read.Body.String())
		}
	}
}

func TestGlobalProfileExtensionAPIUsesIndependentValidatedFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	handler := newGateway(config{gateway: "/app/clash-for-fnos", profilesFile: filepath.Join(root, "profiles.json"), profileDir: filepath.Join(root, "profiles")})
	endpoint := "/app/clash-for-fnos/api/profiles/global/extensions/override"

	getDefault := httptest.NewRecorder()
	handler.ServeHTTP(getDefault, httptest.NewRequest(http.MethodGet, endpoint, nil))
	if getDefault.Code != http.StatusOK || !strings.Contains(getDefault.Body.String(), `"customized":false`) {
		t.Fatalf("default status=%d body=%s", getDefault.Code, getDefault.Body.String())
	}

	content := "mode: rule\nallow-lan: false\n"
	saved := httptest.NewRecorder()
	handler.ServeHTTP(saved, httptest.NewRequest(http.MethodPut, endpoint, strings.NewReader(`{"content":`+mustJSON(content)+`}`)))
	if saved.Code != http.StatusOK {
		t.Fatalf("save status=%d body=%s", saved.Code, saved.Body.String())
	}
	body, err := os.ReadFile(filepath.Join(root, "profiles", "_global.override.yaml"))
	if err != nil || string(body) != content {
		t.Fatalf("saved content=%q err=%v", body, err)
	}

	unsupported := httptest.NewRecorder()
	handler.ServeHTTP(unsupported, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/profiles/global/extensions/rules", nil))
	if unsupported.Code != http.StatusNotFound {
		t.Fatalf("unsupported status=%d body=%s", unsupported.Code, unsupported.Body.String())
	}

	scriptEndpoint := "/app/clash-for-fnos/api/profiles/global/extensions/script"
	script := "function main(config) { config.globalScript = true; return config; }\n"
	saved = httptest.NewRecorder()
	handler.ServeHTTP(saved, httptest.NewRequest(http.MethodPut, scriptEndpoint, strings.NewReader(`{"content":`+mustJSON(script)+`,"apply":true}`)))
	if saved.Code != http.StatusOK {
		t.Fatalf("save script status=%d body=%s", saved.Code, saved.Body.String())
	}
	if !strings.Contains(saved.Body.String(), `"applied":false`) {
		t.Fatalf("save script without a current profile should report unapplied: %s", saved.Body.String())
	}
	body, err = os.ReadFile(filepath.Join(root, "profiles", "_global.script.js"))
	if err != nil || string(body) != script {
		t.Fatalf("saved script content=%q err=%v", body, err)
	}
	readScript := httptest.NewRecorder()
	handler.ServeHTTP(readScript, httptest.NewRequest(http.MethodGet, scriptEndpoint, nil))
	if readScript.Code != http.StatusOK || !strings.Contains(readScript.Body.String(), `"customized":true`) || !strings.Contains(readScript.Body.String(), "globalScript") {
		t.Fatalf("read script status=%d body=%s", readScript.Code, readScript.Body.String())
	}
	if overrideBody, readErr := os.ReadFile(filepath.Join(root, "profiles", "_global.override.yaml")); readErr != nil || string(overrideBody) != content {
		t.Fatalf("global override changed after saving script: %q err=%v", overrideBody, readErr)
	}
}

func TestProfileExtensionSaveAndApplyStartsActivationJob(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	handler := newGateway(config{
		gateway:           "/app/clash-for-fnos",
		profilesFile:      filepath.Join(root, "profiles.json"),
		profileDir:        filepath.Join(root, "profiles"),
		settingsFile:      filepath.Join(root, "settings.json"),
		managedConfigFile: filepath.Join(root, "config.yaml"),
		configMetaFile:    filepath.Join(root, "config-meta.json"),
		backupDir:         filepath.Join(root, "backups"),
		privilegedSocket:  filepath.Join(root, "missing-helper.sock"),
	})
	imported := httptest.NewRecorder()
	handler.ServeHTTP(imported, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/profiles/import", strings.NewReader(`{"Name":"立即应用测试","Content":"rules: [MATCH,DIRECT]\n"}`)))
	if imported.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", imported.Code, imported.Body.String())
	}
	var item map[string]any
	if err := json.Unmarshal(imported.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	id := item["id"].(string)
	content := "prepend:\n  - DOMAIN-SUFFIX,example.com,DIRECT\nappend: []\ndelete: []\n"
	saved := httptest.NewRecorder()
	handler.ServeHTTP(saved, httptest.NewRequest(http.MethodPut, "/app/clash-for-fnos/api/profiles/"+id+"/extensions/rules", strings.NewReader(`{"content":`+mustJSON(content)+`,"apply":true}`)))
	if saved.Code != http.StatusAccepted {
		t.Fatalf("save and apply status=%d body=%s", saved.Code, saved.Body.String())
	}
	var job profileJob
	if err := json.Unmarshal(saved.Body.Bytes(), &job); err != nil || job.ID == "" || job.Operation != "activate" {
		t.Fatalf("unexpected activation job: %#v err=%v", job, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		handler.jobMu.Lock()
		state := handler.profileJobs[job.ID].State
		handler.jobMu.Unlock()
		if state != "running" {
			if body, err := os.ReadFile(filepath.Join(root, "profiles", id+".rules.yaml")); err != nil || string(body) != content {
				t.Fatalf("saved extension content=%q err=%v", body, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("activation job did not finish")
}

func mustJSON(value string) string {
	body, _ := json.Marshal(value)
	return string(body)
}
