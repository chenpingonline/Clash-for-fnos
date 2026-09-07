package mihomo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSettings(t *testing.T, value any) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "settings.json")
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestClientUsesControllerAndBearerSecret(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connections" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatalf("unexpected request: path=%q authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	client := Client{SettingsFile: writeSettings(t, Settings{Controller: server.URL, Secret: "test-secret"})}
	response, err := client.Do(context.Background(), http.MethodGet, "/connections", nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}

func TestClientPreservesMihomoStatusAndMessage(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"invalid mode"}`)
	}))
	defer server.Close()
	client := Client{SettingsFile: writeSettings(t, Settings{Controller: server.URL})}
	_, err := client.Do(context.Background(), http.MethodPatch, "/configs", nil, time.Second)
	apiError, ok := err.(*APIError)
	if !ok || apiError.Status != http.StatusBadRequest || apiError.Message != "Mihomo 400: invalid mode" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestSettingsRejectUnsupportedControllerScheme(t *testing.T) {
	t.Parallel()
	client := Client{SettingsFile: writeSettings(t, Settings{Controller: "file:///tmp/mihomo.sock"})}
	if _, err := client.LoadSettings(); err == nil {
		t.Fatal("expected unsupported scheme to fail")
	}
}

func TestSettingsDefaultToPersistSelections(t *testing.T) {
	t.Parallel()
	client := Client{SettingsFile: writeSettings(t, map[string]any{"controller": defaultController})}
	settings, err := client.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.PersistSelections {
		t.Fatal("persistSelections should default to true")
	}
	client.SettingsFile = writeSettings(t, map[string]any{"controller": defaultController, "persistSelections": false})
	settings, err = client.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.PersistSelections {
		t.Fatal("explicit persistSelections=false should be preserved")
	}
}
