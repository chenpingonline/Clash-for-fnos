package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
)

const trafficTotalsInterval = 30 * time.Second

type trafficTotals struct {
	Upload   uint64 `json:"upload"`
	Download uint64 `json:"download"`
}

type trafficTotalsState struct {
	trafficTotals
	LastUpload   uint64 `json:"lastUpload"`
	LastDownload uint64 `json:"lastDownload"`
	Initialized  bool   `json:"initialized"`
}

type trafficTotalsTracker struct {
	file  string
	mu    sync.Mutex
	state trafficTotalsState
}

func newTrafficTotalsTracker(file string) *trafficTotalsTracker {
	tracker := &trafficTotalsTracker{file: file}
	if file == "" {
		return tracker
	}
	body, err := os.ReadFile(file)
	if err == nil {
		if decodeErr := json.Unmarshal(body, &tracker.state); decodeErr != nil {
			log.Printf("忽略损坏的流量累计文件 %s: %v", file, decodeErr)
			tracker.state = trafficTotalsState{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("读取流量累计文件 %s 失败: %v", file, err)
	}
	return tracker
}

func counterValue(value any) uint64 {
	switch typed := value.(type) {
	case uint64:
		return typed
	case uint:
		return uint64(typed)
	case int:
		if typed >= 0 {
			return uint64(typed)
		}
	case int64:
		if typed >= 0 {
			return uint64(typed)
		}
	case float64:
		if typed >= 0 && !math.IsNaN(typed) && !math.IsInf(typed, 0) {
			return uint64(typed)
		}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil && parsed >= 0 {
			return uint64(parsed)
		}
	}
	return 0
}

func counterDelta(current, previous uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	// Mihomo counters restart from zero with the Core process.
	return current
}

func (t *trafficTotalsTracker) Observe(rawUpload, rawDownload any) trafficTotals {
	upload, download := counterValue(rawUpload), counterValue(rawDownload)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.state.Initialized {
		t.state.Upload, t.state.Download = upload, download
		t.state.Initialized = true
	} else {
		t.state.Upload += counterDelta(upload, t.state.LastUpload)
		t.state.Download += counterDelta(download, t.state.LastDownload)
	}
	t.state.LastUpload, t.state.LastDownload = upload, download
	if err := t.saveLocked(); err != nil {
		log.Printf("保存流量累计失败: %v", err)
	}
	return t.state.trafficTotals
}

func (t *trafficTotalsTracker) saveLocked() error {
	if t.file == "" {
		return nil
	}
	body, err := json.MarshalIndent(t.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.file), 0o700); err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.%d.tmp", t.file, os.Getpid())
	if err := os.WriteFile(temporary, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, t.file); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (t *trafficTotalsTracker) sample(ctx context.Context, client *mihomo.Client) {
	payload, err := mihomoJSON(ctx, client, "/connections")
	if err != nil {
		return
	}
	t.Observe(payload["uploadTotal"], payload["downloadTotal"])
}

func (t *trafficTotalsTracker) Run(ctx context.Context, client *mihomo.Client) {
	t.sample(ctx, client)
	ticker := time.NewTicker(trafficTotalsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.sample(ctx, client)
		}
	}
}
