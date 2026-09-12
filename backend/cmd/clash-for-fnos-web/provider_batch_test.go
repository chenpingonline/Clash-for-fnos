package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestProxyProviderBatchUpdatesEachUniqueProvider(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	requests := make([]string, 0, 2)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/broken") {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"message":"download failed"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer controller.Close()

	gateway := newGateway(config{settingsFile: writeGatewaySettings(t, controller.URL)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/providers/update-all", strings.NewReader(`{"names":["fast","fast","broken"]}`))
	gateway.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result struct {
		Success int `json:"success"`
		Failed  int `json:"failed"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Success != 1 || result.Failed != 1 {
		t.Fatalf("result=%+v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(requests, ",") != "/providers/proxies/fast,/providers/proxies/broken" {
		t.Fatalf("requests=%v", requests)
	}
}

func TestRuleProviderBatchReportsDirectFallback(t *testing.T) {
	t.Parallel()
	attempts := 0
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/providers/rules/geo":
			attempts++
			if attempts == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"message":"temporary failure"}`)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/configs":
			_, _ = io.WriteString(w, `{"mode":"rule"}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/configs":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	gateway := newGateway(config{settingsFile: writeGatewaySettings(t, controller.URL), rulesSnapshotFile: t.TempDir() + "/rules.json"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/rule-providers/update-all", strings.NewReader(`{"names":["geo"]}`))
	gateway.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"fallback":1`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
