package mihomolog

import (
	"path/filepath"
	"testing"
)

func TestHistoryFiltersLevelAndLimit(t *testing.T) {
	manager := New(filepath.Join(t.TempDir(), "mihomo.log"))
	for _, record := range []Record{{Time: "1", Level: "info", Message: "a"}, {Time: "2", Level: "warning", Message: "b"}, {Time: "3", Level: "error", Message: "c"}} {
		if err := manager.Append(record); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := manager.History("warning", 1)
	if err != nil {
		t.Fatal(err)
	}
	items := payload["items"].([]Record)
	if len(items) != 1 || items[0].Message != "c" {
		t.Fatalf("unexpected items: %#v", items)
	}
	if err := manager.Clear(); err != nil {
		t.Fatal(err)
	}
	payload, _ = manager.History("debug", 10)
	if len(payload["items"].([]Record)) != 0 {
		t.Fatal("history was not cleared")
	}
}

func TestNormalizeStructuredAndPlainLogs(t *testing.T) {
	record, ok := Normalize([]byte(`{"time":"now","type":"warn","payload":"message"}`))
	if !ok || record.Level != "warning" || record.Message != "message" {
		t.Fatalf("unexpected record: %#v", record)
	}
	record, ok = Normalize([]byte("plain"))
	if !ok || record.Level != "info" || record.Message != "plain" {
		t.Fatalf("unexpected plain record: %#v", record)
	}
}
