package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeProxyPrefersUsableExplicitPorts(t *testing.T) {
	tests := []struct {
		name, expectedURL, expectedVia string
		configs                        map[string]any
	}{
		{name: "mixed", configs: map[string]any{"mixed-port": float64(7890), "port": float64(7891)}, expectedURL: "http://127.0.0.1:7890", expectedVia: "mixed"},
		{name: "http", configs: map[string]any{"mixed-port": float64(0), "port": "7891"}, expectedURL: "http://127.0.0.1:7891", expectedVia: "http"},
		{name: "socks", configs: map[string]any{"socks-port": json.Number("7892")}, expectedURL: "socks5://127.0.0.1:7892", expectedVia: "socks5"},
		{name: "direct", configs: map[string]any{"mixed-port": float64(70000)}, expectedVia: "direct"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proxyURL, via := runtimeProxy(test.configs)
			if via != test.expectedVia {
				t.Fatalf("via = %q", via)
			}
			if test.expectedURL == "" {
				if proxyURL != nil {
					t.Fatalf("proxy = %s", proxyURL)
				}
				return
			}
			if proxyURL == nil || proxyURL.String() != test.expectedURL {
				t.Fatalf("proxy = %v", proxyURL)
			}
		})
	}
}

func TestExitLocationReturnsNormalizedLiveData(t *testing.T) {
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configs" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"mixed-port":0,"port":0,"socks-port":0}`))
	}))
	defer controller.Close()
	location := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"ip":"103.123.45.67","country":"中国香港","country_code":"hk","region":"中西区","city":"香港","timezone":{"id":"Asia/Hong_Kong","utc":"+08:00"}}`))
	}))
	defer location.Close()

	handler := newGateway(config{
		gateway:           "/app/clash-for-fnos",
		settingsFile:      writeGatewaySettings(t, controller.URL),
		trafficTotalsFile: filepath.Join(t.TempDir(), "traffic-totals.json"),
		exitLocationURL:   location.URL,
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/exit-location", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	for _, expected := range []string{`"ip":"103.123.45.67"`, `"country":"中国香港"`, `"countryCode":"HK"`, `"timezone":"Asia/Hong_Kong"`, `"via":"direct"`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("missing %s in %s", expected, recorder.Body.String())
		}
	}
}
