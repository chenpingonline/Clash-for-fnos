package appsettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdatePreservesUnrelatedSettings(t *testing.T) {
	file := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(file, []byte(`{"dnsOverrideEnabled":true,"secret":"old"}`), 0o600)
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
