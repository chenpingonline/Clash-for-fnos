package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDelayBatchContinuesAfterStartRequestAndUsesTenWorkers(t *testing.T) {
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
	start := httptest.NewRecorder()
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/app/clash-for-fnos/api/delays", bytes.NewReader(body)))
	if start.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", start.Code, start.Body.String())
	}
	var started delayBatchStatus
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.ID == "" || started.State != "running" || len(started.Names) != len(names) {
		t.Fatalf("unexpected start response: %#v", started)
	}

	deadline := time.Now().Add(2 * time.Second)
	for peak.Load() < maxDelayBatchConcurrency && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/delays", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"state":"running"`) {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
	connections := httptest.NewRecorder()
	handler.ServeHTTP(connections, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/connections", nil))
	if connections.Code != http.StatusOK || connections.Body.String() != `{"connections":[]}` {
		t.Fatalf("connections were blocked by delay workers: status=%d body=%s", connections.Code, connections.Body.String())
	}

	close(release)
	deadline = time.Now().Add(2 * time.Second)
	for {
		status = httptest.NewRecorder()
		handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/app/clash-for-fnos/api/delays", nil))
		var completed delayBatchStatus
		if err := json.Unmarshal(status.Body.Bytes(), &completed); err != nil {
			t.Fatal(err)
		}
		if completed.State == "done" {
			if len(completed.Results) != len(names) {
				t.Fatalf("received %d results, want %d", len(completed.Results), len(names))
			}
			for _, result := range completed.Results {
				if result.State != "done" || result.Delay != 12 {
					t.Fatalf("unexpected result: %#v", result)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("persistent delay job did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if peak.Load() != maxDelayBatchConcurrency {
		t.Fatalf("peak concurrency = %d, want %d", peak.Load(), maxDelayBatchConcurrency)
	}
}

func TestDelayBatchStreamRestoresRunningJobAndPublishesCompletion(t *testing.T) {
	gateway := newGateway(config{})
	now := time.Now().UnixMilli()
	gateway.delayJob = &delayBatchJob{ID: "delay-1", State: "running", Names: []string{"a"}, CreatedAt: now, UpdatedAt: now}
	server := httptest.NewServer(gateway)
	defer server.Close()

	response, err := http.Get(server.URL + "/api/delays/delay-1/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	readStatus := func() delayBatchStatus {
		t.Helper()
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		var status delayBatchStatus
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &status); err != nil {
			t.Fatal(err)
		}
		_, _ = reader.ReadString('\n')
		return status
	}
	if initial := readStatus(); initial.State != "running" || initial.Names[0] != "a" {
		t.Fatalf("initial=%#v", initial)
	}

	gateway.delayMu.Lock()
	gateway.delayResults["a"] = delayBatchResult{Name: "a", Delay: 18, State: "done"}
	gateway.delayJob.State = "done"
	gateway.publishDelayStatusLocked()
	gateway.delayMu.Unlock()
	if completed := readStatus(); completed.State != "done" || len(completed.Results) != 1 {
		t.Fatalf("completed=%#v", completed)
	}
}
