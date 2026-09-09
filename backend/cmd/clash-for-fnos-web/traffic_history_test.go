package main

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

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
)

func TestTrafficHistoryPersistsAndPrunesTenMinuteWindow(t *testing.T) {
	file := filepath.Join(t.TempDir(), "traffic-history.json")
	now := time.Now().Truncate(time.Second)
	tracker := newTrafficHistoryTracker(file)
	tracker.now = func() time.Time { return now.Add(-11 * time.Minute) }
	tracker.Add(1, 2)
	tracker.now = func() time.Time { return now.Add(-5 * time.Minute) }
	tracker.Add(3, 4)
	tracker.now = func() time.Time { return now }
	tracker.Add(5, 6)
	if err := tracker.Save(); err != nil {
		t.Fatal(err)
	}
	reloaded := newTrafficHistoryTracker(file)
	samples := reloaded.Snapshot()
	if len(samples) != 2 || samples[0].Up != 3 || samples[1].Down != 6 {
		t.Fatalf("samples = %#v", samples)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("history file mode = %v", info.Mode().Perm())
	}
}

func TestTrafficHistoryCapsSamplesAndServesAPI(t *testing.T) {
	tracker := newTrafficHistoryTracker("")
	now := time.Now()
	for index := 0; index < trafficHistoryLimit+20; index++ {
		current := now.Add(time.Duration(index) * time.Millisecond)
		tracker.now = func() time.Time { return current }
		tracker.Add(uint64(index), uint64(index+1))
	}
	handler := newGateway(config{gateway: "/app/clash-for-fnos"})
	handler.trafficHistory = tracker
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/traffic-history", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload trafficHistoryState
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Samples) != trafficHistoryLimit || payload.Samples[0].Up != 20 {
		t.Fatalf("samples = %d, first = %#v", len(payload.Samples), payload.Samples[0])
	}
}

func TestTrafficHistoryCollectsMihomoStream(t *testing.T) {
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/traffic" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "{\"up\":12,\"down\":34}\ninvalid\n{\"up\":56,\"down\":78}\n")
	}))
	defer controller.Close()
	tracker := newTrafficHistoryTracker("")
	client := &mihomo.Client{SettingsFile: writeGatewaySettings(t, controller.URL)}
	if err := tracker.collectOnce(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	samples := tracker.Snapshot()
	if len(samples) != 2 || samples[0].Up != 12 || samples[1].Down != 78 {
		t.Fatalf("samples = %#v", samples)
	}
}
