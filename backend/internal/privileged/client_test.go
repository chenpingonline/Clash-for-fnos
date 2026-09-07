package privileged

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetJSONOverUnixSocket(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "cff-helper-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	socket := filepath.Join(directory, "helper.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/proxy-group-order" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"order":["A"]}`))
	})}
	go server.Serve(listener)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	var payload struct {
		Order []string `json:"order"`
	}
	if err := (Client{SocketPath: socket}).GetJSON(context.Background(), "/config/proxy-group-order", &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Order) != 1 || payload.Order[0] != "A" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}
