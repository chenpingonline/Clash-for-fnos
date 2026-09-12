package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
)

type dashboardStreamEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

func (g *gateway) streamDashboard(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "当前服务不支持流式响应"})
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events := make(chan dashboardStreamEvent, 32)
	go streamDashboardMihomo(ctx, client, "/traffic", "traffic", events)
	go streamDashboardMihomo(ctx, client, "/memory", "memory", events)
	go g.streamDashboardConnections(ctx, client, events)
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			body, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err = fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func streamDashboardMihomo(ctx context.Context, client *mihomo.Client, apiPath, eventType string, events chan<- dashboardStreamEvent) {
	for ctx.Err() == nil {
		response, err := client.Do(ctx, http.MethodGet, apiPath, nil, 0)
		if err == nil {
			scanner := bufio.NewScanner(response.Body)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for scanner.Scan() {
				line := append([]byte(nil), scanner.Bytes()...)
				if !json.Valid(line) {
					continue
				}
				select {
				case events <- dashboardStreamEvent{Type: eventType, Data: line}:
				case <-ctx.Done():
					response.Body.Close()
					return
				}
			}
			response.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (g *gateway) streamDashboardConnections(ctx context.Context, client *mihomo.Client, events chan<- dashboardStreamEvent) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		connections, err := mihomoJSON(ctx, client, "/connections")
		if err == nil {
			stats := g.connectionStats(connections, connections["memory"])
			if body, marshalErr := json.Marshal(stats); marshalErr == nil {
				select {
				case events <- dashboardStreamEvent{Type: "connections", Data: body}:
				case <-ctx.Done():
					return
				}
			}
		} else {
			select {
			case events <- dashboardStreamEvent{Type: "connections-error"}:
			case <-ctx.Done():
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
