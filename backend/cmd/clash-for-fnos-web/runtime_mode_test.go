package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRuntimeModeTransaction(t *testing.T) {
	for _, tc := range []struct {
		name, mode                    string
		reject, mismatch, persistFail bool
	}{
		{name: "rule", mode: "rule"}, {name: "global", mode: "global"}, {name: "direct", mode: "direct"},
		{name: "core rejected", mode: "global", reject: true}, {name: "readback mismatch", mode: "global", mismatch: true},
		{name: "persistence failed", mode: "global", persistFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			live := "rule"
			disk := "rule"
			override := "rule"
			activated := false
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.Path != "/configs" {
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodGet {
					writeJSON(w, 200, map[string]string{"mode": live})
					return
				}
				var patch struct {
					Mode string `json:"mode"`
				}
				_ = json.NewDecoder(r.Body).Decode(&patch)
				if !activated && patch.Mode != "rule" {
					t.Error("core patched before persistence")
				}
				if patch.Mode == tc.mode && tc.reject {
					http.Error(w, "rejected", 500)
					return
				}
				if !tc.mismatch {
					live = patch.Mode
				}
				w.WriteHeader(204)
			}))
			defer core.Close()
			socket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/config/mode":
					writeJSON(w, 200, map[string]string{"txId": "mode-test"})
				case "/config/activate":
					if tc.persistFail {
						http.Error(w, "disk full", 500)
						return
					}
					activated = true
					disk = tc.mode
					override = tc.mode
					writeJSON(w, 200, map[string]bool{"ok": true})
				case "/config/rollback":
					disk = "rule"
					override = "rule"
					writeJSON(w, 200, map[string]bool{"ok": true})
				case "/config/commit":
					writeJSON(w, 200, map[string]bool{"ok": true})
				default:
					http.NotFound(w, r)
				}
			}))
			root := t.TempDir()
			file := filepath.Join(root, "config.yaml")
			_ = os.WriteFile(file, []byte("mode: rule\n"), 0600)
			g := newGateway(config{settingsFile: writeGatewaySettings(t, core.URL), privilegedSocket: socket, managedConfigFile: file})
			recorder := httptest.NewRecorder()
			g.ServeHTTP(recorder, httptest.NewRequest(http.MethodPatch, "/api/runtime-config", strings.NewReader(`{"mode":"`+tc.mode+`"}`)))
			mu.Lock()
			defer mu.Unlock()
			failed := tc.reject || tc.mismatch || tc.persistFail
			saved, _ := os.ReadFile(file)
			if failed {
				if recorder.Code == 200 || live != "rule" || disk != "rule" || override != "rule" || string(saved) != "mode: rule\n" {
					t.Fatalf("failed rollback status=%d live=%s disk=%s override=%s saved=%s", recorder.Code, live, disk, override, saved)
				}
			} else if recorder.Code != 200 || live != tc.mode || disk != tc.mode || override != tc.mode || !strings.Contains(string(saved), "mode: "+tc.mode) {
				t.Fatalf("not persisted: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}
