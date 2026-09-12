package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCoreBootstrapStatusStreamProxiesHelperState(t *testing.T) {
	helperSocket := startTunHelper(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bootstrap/status" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"state": "downloading", "message": "正在下载 Core", "progress": 42})
	}))
	gateway := newGateway(config{privilegedSocket: helperSocket})
	server := httptest.NewServer(gateway)
	defer server.Close()

	response, err := http.Get(server.URL + "/api/core/bootstrap/status/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type=%q", response.Header.Get("Content-Type"))
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, `"state":"downloading"`) || !strings.Contains(line, `"progress":42`) {
		t.Fatalf("event=%s", line)
	}
}
