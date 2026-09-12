package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDashboardStreamCombinesTrafficMemoryAndConnections(t *testing.T) {
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/traffic":
			_, _ = w.Write([]byte("{\"up\":12,\"down\":34}\n"))
		case "/memory":
			_, _ = w.Write([]byte("{\"inuse\":4096}\n"))
		case "/connections":
			_, _ = w.Write([]byte("{\"connections\":[],\"uploadTotal\":5,\"downloadTotal\":6,\"memory\":2048}"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()
	gateway := newGateway(config{settingsFile: writeGatewaySettings(t, controller.URL)})
	server := httptest.NewServer(gateway)
	defer server.Close()

	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(server.URL + "/api/stream/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	found := map[string]bool{}
	reader := bufio.NewReader(response.Body)
	for len(found) < 3 {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event dashboardStreamEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "traffic" || event.Type == "memory" || event.Type == "connections" {
			found[event.Type] = true
		}
	}
}
