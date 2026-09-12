package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizedDelayNames(t *testing.T) {
	names, err := normalizedDelayNames([]string{" a ", "b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(names) != "[a b]" {
		t.Fatalf("unexpected names: %v", names)
	}
	if _, err := normalizedDelayNames([]string{""}); err == nil {
		t.Fatal("accepted an empty node name")
	}
}

func TestDelayBatchUsesOneStreamAndTenBackendWorkers(t *testing.T) {
	var active atomic.Int32
	var peak atomic.Int32
	release := make(chan struct{})
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gateway-secret" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/connections" {
			_, _ = io.WriteString(w, `{"connections":[]}`)
			return
		}
		current := active.Add(1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		<-release
		active.Add(-1)
		_, _ = io.WriteString(w, `{"delay":12}`)
	}))
	defer controller.Close()

	handler := newGateway(config{
		publicDir:    t.TempDir(),
		gateway:      "/app/clash-for-fnos",
		settingsFile: writeGatewaySettings(t, controller.URL),
	})
	names := make([]string, 12)
	for index := range names {
		names[index] = fmt.Sprintf("node-%d", index)
	}
	body, _ := json.Marshal(delayBatchRequest{Names: names})
	request := httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/delays", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for peak.Load() < maxDelayBatchConcurrency && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	connections := httptest.NewRecorder()
	handler.ServeHTTP(connections, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/connections", nil))
	if connections.Code != http.StatusOK || connections.Body.String() != `{"connections":[]}` {
		t.Fatalf("connections were blocked by delay workers: status=%d body=%s", connections.Code, connections.Body.String())
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("batch request did not finish")
	}
	if peak.Load() != maxDelayBatchConcurrency {
		t.Fatalf("peak concurrency = %d, want %d", peak.Load(), maxDelayBatchConcurrency)
	}
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/x-ndjson; charset=utf-8" {
		t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	seen := make(map[string]delayBatchResult)
	scanner := bufio.NewScanner(recorder.Body)
	for scanner.Scan() {
		var result delayBatchResult
		if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		seen[result.Name] = result
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(names) {
		t.Fatalf("received %d results, want %d: %s", len(seen), len(names), recorder.Body.String())
	}
	for _, name := range names {
		if result := seen[name]; result.State != "done" || result.Delay != 12 {
			t.Fatalf("unexpected result for %s: %#v", name, result)
		}
	}
}
