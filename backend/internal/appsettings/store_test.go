package appsettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdatePreservesUnrelatedSettings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(file, []byte(`{"dnsOverrideEnabled":true,"secret":"old","applyManagedConfigOnStart":true}`), 0o600)
	store := &Store{File: file}
	controller := "http://127.0.0.1:9091/"
	persist := false
	err := store.WithDocument(func(document map[string]any) error {
		return Apply(document, Update{Controller: &controller, PersistSelections: &persist, ClearSecret: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	public, err = store.ReadPublic()
	if err != nil {
		t.Fatal(err)
	}
	if public["controller"] != "http://127.0.0.1:9091" || public["hasSecret"] != false || public["persistSelections"] != false {
		t.Fatalf("unexpected public settings: %#v", public)
	}
	body, _ := os.ReadFile(file)
	if string(body) == "" || !contains(string(body), `"dnsOverrideEnabled": true`) {
		t.Fatalf("unrelated setting lost: %s", body)
	}
	if contains(string(body), `"applyManagedConfigOnStart"`) {
		t.Fatalf("legacy unused setting was not removed: %s", body)
	}
}
func contains(value, part string) bool {
	return len(value) >= len(part) && func() bool {
		for i := 0; i+len(part) <= len(value); i++ {
			if value[i:i+len(part)] == part {
				return true
			}
		}
		return false
	}()
}

func TestSyncControllerUsesManagedCredentials(t *testing.T) {
	document := map[string]any{}
	SyncController(document, map[string]any{"mode": "managed", "managedController": "http://127.0.0.1:9191", "managedSecret": "secret"})
	if document["controller"] != "http://127.0.0.1:9191" || document["secret"] != "secret" {
		t.Fatalf("unexpected document: %#v", document)
	}
}

func TestReadSecretIsSeparateFromPublicSettings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(file, []byte(`{"secret":"show-on-demand"}`), 0o600)
	store := &Store{File: file}
	public, err := store.ReadPublic()
	if err != nil {
		t.Fatal(err)
	}
	if _, exposed := public["secret"]; exposed || public["hasSecret"] != true {
		t.Fatalf("unexpected public settings: %#v", public)
	}
	secret, err := store.ReadSecret()
	if err != nil || secret != "show-on-demand" {
		t.Fatalf("secret=%q err=%v", secret, err)
	}
}

func TestAppUpdateNotificationsDefaultOnAndCanBeDisabled(t *testing.T) {
	document := map[string]any{}
	if Public(document)["notifyAppUpdates"] != true {
		t.Fatalf("app update notifications should default to enabled: %#v", Public(document))
	}
	disabled := false
	if err := Apply(document, Update{NotifyAppUpdates: &disabled}); err != nil {
		t.Fatal(err)
	}
	if Public(document)["notifyAppUpdates"] != false || document["notifyAppUpdates"] != false {
		t.Fatalf("app update notification preference was not persisted: %#v", document)
	}
}
