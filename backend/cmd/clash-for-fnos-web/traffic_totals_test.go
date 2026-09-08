package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrafficTotalsPersistAcrossCoreAndWebRestarts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "traffic-totals.json")
	tracker := newTrafficTotalsTracker(file)
	if totals := tracker.Observe(float64(100), float64(200)); totals.Upload != 100 || totals.Download != 200 {
		t.Fatalf("initial totals = %#v", totals)
	}
	if totals := tracker.Observe(float64(140), float64(260)); totals.Upload != 140 || totals.Download != 260 {
		t.Fatalf("running totals = %#v", totals)
	}

	// A lower raw counter means the Mihomo process restarted.
	if totals := tracker.Observe(float64(10), float64(20)); totals.Upload != 150 || totals.Download != 280 {
		t.Fatalf("totals after core restart = %#v", totals)
	}

	reloaded := newTrafficTotalsTracker(file)
	if totals := reloaded.Observe(float64(10), float64(20)); totals.Upload != 150 || totals.Download != 280 {
		t.Fatalf("totals after web restart = %#v", totals)
	}
	if totals := reloaded.Observe(float64(15), float64(30)); totals.Upload != 155 || totals.Download != 290 {
		t.Fatalf("continued totals = %#v", totals)
	}
}

func TestTrafficTotalsFileIsPrivateAndValid(t *testing.T) {
	file := filepath.Join(t.TempDir(), "nested", "traffic-totals.json")
	newTrafficTotalsTracker(file).Observe(1, 2)
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	var state trafficTotalsState
	body, _ := os.ReadFile(file)
	if err := json.Unmarshal(body, &state); err != nil || !state.Initialized || state.Upload != 1 || state.Download != 2 {
		t.Fatalf("state = %#v, err = %v", state, err)
	}
}

func TestStatusReturnsPersistentTrafficTotals(t *testing.T) {
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_, _ = io.WriteString(w, `{"version":"1.2.3"}`)
		case "/configs":
			_, _ = io.WriteString(w, `{}`)
		case "/connections":
			_, _ = io.WriteString(w, `{"uploadTotal":10,"downloadTotal":20,"memory":30,"connections":[{}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()
	directory := t.TempDir()
	handler := newGateway(config{
		gateway:           "/app/clash-for-fnos",
		settingsFile:      writeGatewaySettings(t, controller.URL),
		trafficTotalsFile: filepath.Join(directory, "traffic-totals.json"),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{`"count":1`, `"uploadTotal":10`, `"downloadTotal":20`, `"memory":30`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %s in %s", expected, body)
		}
	}
}

func TestConnectionStatsReturnsLightweightLiveValues(t *testing.T) {
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/connections" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"uploadTotal":40,"downloadTotal":50,"memory":60,"connections":[{},{}]}`)
	}))
	defer controller.Close()
	handler := newGateway(config{
		gateway:           "/app/clash-for-fnos",
		settingsFile:      writeGatewaySettings(t, controller.URL),
		trafficTotalsFile: filepath.Join(t.TempDir(), "traffic-totals.json"),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/connection-stats", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); body != `{"count":2,"downloadTotal":50,"memory":60,"uploadTotal":40}` {
		t.Fatalf("body = %s", body)
	}
}
