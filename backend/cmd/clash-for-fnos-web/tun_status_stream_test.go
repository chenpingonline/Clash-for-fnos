package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTunStatusStreamPublishesOnlyChangedProgress(t *testing.T) {
	gateway := newGateway(config{})
	server := httptest.NewServer(gateway)
	defer server.Close()

	response, err := http.Get(server.URL + "/api/network/tun/status/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type=%q", response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	readStatus := func() tunOperationStatus {
		t.Helper()
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		var status tunOperationStatus
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &status); err != nil {
			t.Fatal(err)
		}
		_, _ = reader.ReadString('\n')
		return status
	}
	if initial := readStatus(); initial.Active {
		t.Fatalf("initial=%+v", initial)
	}

	gateway.setTunOperation(true, true, "validate")
	if update := readStatus(); !update.Active || update.Stage != "validate" || update.Message == "" {
		t.Fatalf("update=%+v", update)
	}
}
