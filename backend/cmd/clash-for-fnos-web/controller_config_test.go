package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestProfileApplicationPreservesLiveController(t *testing.T) {
	for _, tc := range []struct {
		name, incoming, secret  string
		failApply, failRollback bool
	}{
		{name: "missing secret", secret: "live-secret"},
		{name: "different secret and address", incoming: "secret: subscription-secret\nexternal-controller: 127.0.0.1:1\n", secret: "live-secret"},
		{name: "empty live secret", incoming: "secret: subscription-secret\n"},
		{name: "quoted secret", incoming: "secret: wrong\n", secret: "a: b # c\"d"},
		{name: "rollback failure reported", secret: "live-secret", failApply: true, failRollback: true},
		{name: "rollback", incoming: "secret: wrong\n", secret: "live-secret", failApply: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var applies atomic.Int32
			var versions atomic.Int32
			var mu sync.Mutex
			var disk, candidate, old string
			var settingsFile string
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := ""
				if tc.secret != "" {
					want = "Bearer " + tc.secret
				}
				if r.Header.Get("Authorization") != want {
					t.Errorf("incorrect Controller credentials")
					w.WriteHeader(401)
					return
				}
				switch r.URL.Path {
				case "/version":
					versions.Add(1)
					writeJSON(w, 200, map[string]string{"version": "test"})
				case "/configs":
					n := applies.Add(1)
					var body struct {
						Payload string `json:"payload"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					var config map[string]any
					if err := yaml.Unmarshal([]byte(body.Payload), &config); err != nil {
						t.Error(err)
					}
					if n == 1 && config["secret"] != tc.secret {
						t.Error("subscription replaced live secret")
					}
					if n == 1 && tc.failApply {
						// A concurrent settings update must not change rollback authentication.
						if err := os.WriteFile(settingsFile, []byte(`{"controller":"http://127.0.0.1:1","secret":"wrong"}`), 0600); err != nil {
							t.Error(err)
						}
						http.Error(w, "injected apply failure", 500)
						return
					}
					if n == 2 && body.Payload != old {
						t.Error("rollback did not restore original config")
					}
					w.WriteHeader(204)
				default:
					http.NotFound(w, r)
				}
			}))
			defer controller.Close()
			encode := func(value map[string]any) string {
				b, err := yaml.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			old = encode(map[string]any{"mixed-port": 7890, "external-controller": strings.TrimPrefix(controller.URL, "http://"), "secret": tc.secret})
			disk = old
			socketDir, err := os.MkdirTemp("/tmp", "cff-auth-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(socketDir)
			socket := filepath.Join(socketDir, "helper.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			helper := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/status":
					var config map[string]any
					if err := yaml.Unmarshal([]byte(disk), &config); err != nil {
						t.Error(err)
					}
					secret, _ := config["secret"].(string)
					writeJSON(w, 200, map[string]any{"mode": "managed", "managedController": "http://" + config["external-controller"].(string), "managedSecret": secret})
				case "/config/active-raw":
					writeJSON(w, 200, map[string]any{"content": disk, "path": "/test/startup.yaml"})
				case "/config/compose", "/config/sync":
					var body struct {
						Content string `json:"content"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if r.URL.Path == "/config/compose" {
						writeJSON(w, 200, map[string]any{"content": body.Content})
						return
					}
					candidate = body.Content
					var config map[string]any
					if err := yaml.Unmarshal([]byte(candidate), &config); err != nil {
						t.Error(err)
					}
					if config["secret"] != tc.secret || config["external-controller"] != strings.TrimPrefix(controller.URL, "http://") {
						t.Error("candidate changed live credentials before validation")
					}
					writeJSON(w, 200, map[string]any{"txId": "test", "effectiveContent": candidate})
				case "/config/activate":
					disk = candidate
					writeJSON(w, 200, map[string]any{"method": "hot-reload"})
				case "/config/rollback":
					if tc.failRollback {
						http.Error(w, "injected rollback failure", 500)
						return
					}
					disk = old
					writeJSON(w, 200, map[string]any{"ok": true})
				case "/config/commit":
					writeJSON(w, 200, map[string]any{"ok": true})
				default:
					http.NotFound(w, r)
				}
			})}
			go helper.Serve(listener)
			defer helper.Close()
			root := t.TempDir()
			settingsFile = filepath.Join(root, "settings.json")
			credentials, _ := json.Marshal(map[string]any{"controller": controller.URL, "secret": tc.secret})
			if err := os.WriteFile(settingsFile, credentials, 0600); err != nil {
				t.Fatal(err)
			}
			managed := filepath.Join(root, "config.yaml")
			if err := os.WriteFile(managed, []byte(old), 0600); err != nil {
				t.Fatal(err)
			}
			g := newGateway(config{settingsFile: settingsFile, managedConfigFile: managed, configMetaFile: filepath.Join(root, "meta.json"), backupDir: filepath.Join(root, "backups"), privilegedSocket: socket, profileDir: root, profilesFile: filepath.Join(root, "profiles.json")})
			raw := "mixed-port: 7891\n" + tc.incoming
			profileFile := filepath.Join(root, "test.yaml")
			if err := os.WriteFile(profileFile, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			_, err = g.activateProfileLocked(context.Background(), &profileState{}, &profile{ID: "test", Name: "test"}, true, nil)
			if tc.failApply {
				if err == nil || !strings.Contains(err.Error(), "injected apply failure") {
					t.Fatalf("expected apply failure: %v", err)
				}
				if applies.Load() != 2 {
					t.Fatalf("expected apply and rollback, got %d", applies.Load())
				}
				if tc.failRollback && !strings.Contains(err.Error(), "启动配置回滚失败") {
					t.Fatalf("rollback error was hidden: %v", err)
				}
				if !tc.failRollback && disk != old {
					t.Error("startup file not restored")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if applies.Load() != 1 || versions.Load() < 2 {
					t.Fatal("missing apply or readiness check")
				}
				saved, _ := os.ReadFile(managed)
				if string(saved) != disk {
					t.Error("persisted config differs from startup config")
				}
				// Re-reading actual disk credentials must still reach the same live Core.
				g.syncControllerSettings(context.Background())
				if err := g.waitController(context.Background(), time.Second); err != nil {
					t.Fatal(err)
				}
			}
			unchanged, _ := os.ReadFile(profileFile)
			if string(unchanged) != raw {
				t.Error("subscription source was modified")
			}
		})
	}
}
