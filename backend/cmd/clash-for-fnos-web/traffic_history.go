package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
)

const (
	trafficHistoryRetention = 10 * time.Minute
	trafficHistoryLimit     = 600
	trafficHistoryFlush     = 10 * time.Second
	trafficHistoryRetry     = time.Second
	trafficHistoryIdle      = 5 * time.Second
)

type trafficHistorySample struct {
	Time int64  `json:"time"`
	Up   uint64 `json:"up"`
	Down uint64 `json:"down"`
}

type trafficHistoryState struct {
	Samples []trafficHistorySample `json:"samples"`
}

type trafficHistoryTracker struct {
	file          string
	saveMu        sync.Mutex
	mu            sync.Mutex
	samples       []trafficHistorySample
	revision      uint64
	savedRevision uint64
	now           func() time.Time
	idleTimeout   time.Duration
	retryDelay    time.Duration
}

func newTrafficHistoryTracker(file string) *trafficHistoryTracker {
	tracker := &trafficHistoryTracker{file: file, now: time.Now, idleTimeout: trafficHistoryIdle, retryDelay: trafficHistoryRetry}
	if file == "" {
		return tracker
	}
	body, err := os.ReadFile(file)
	if err == nil {
		var state trafficHistoryState
		if decodeErr := json.Unmarshal(body, &state); decodeErr != nil {
			log.Printf("忽略损坏的实时流量历史文件 %s: %v", file, decodeErr)
		} else {
			tracker.samples = tracker.retained(state.Samples, tracker.now())
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("读取实时流量历史文件 %s 失败: %v", file, err)
	}
	return tracker
}

func (t *trafficHistoryTracker) retained(samples []trafficHistorySample, now time.Time) []trafficHistorySample {
	cutoff := now.Add(-trafficHistoryRetention).UnixMilli()
	kept := make([]trafficHistorySample, 0, len(samples))
	for _, sample := range samples {
		if sample.Time >= cutoff && sample.Time <= now.Add(time.Minute).UnixMilli() {
			kept = append(kept, sample)
		}
	}
	if len(kept) > trafficHistoryLimit {
		kept = kept[len(kept)-trafficHistoryLimit:]
	}
	return kept
}

func (t *trafficHistoryTracker) Add(up, down uint64) trafficHistorySample {
	sample := trafficHistorySample{Time: t.now().UnixMilli(), Up: up, Down: down}
	t.mu.Lock()
	t.samples = append(t.samples, sample)
	t.samples = t.retained(t.samples, t.now())
	t.revision++
	t.mu.Unlock()
	return sample
}

func (t *trafficHistoryTracker) Snapshot() []trafficHistorySample {
	t.mu.Lock()
	t.samples = t.retained(t.samples, t.now())
	result := append([]trafficHistorySample(nil), t.samples...)
	t.mu.Unlock()
	return result
}

func (t *trafficHistoryTracker) Save() error {
	if t.file == "" {
		return nil
	}
	t.saveMu.Lock()
	defer t.saveMu.Unlock()
	t.mu.Lock()
	if t.revision == t.savedRevision {
		t.mu.Unlock()
		return nil
	}
	revision := t.revision
	state := trafficHistoryState{Samples: append([]trafficHistorySample(nil), t.samples...)}
	t.mu.Unlock()
	body, err := json.Marshal(state)
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
	t.mu.Lock()
	if t.savedRevision < revision {
		t.savedRevision = revision
	}
	t.mu.Unlock()
	return nil
}

func (t *trafficHistoryTracker) collectOnce(ctx context.Context, client *mihomo.Client) error {
	streamContext, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	response, err := client.Do(streamContext, http.MethodGet, "/traffic", nil, 0)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	type streamEvent struct {
		up   uint64
		down uint64
		err  error
		done bool
	}
	events := make(chan streamEvent)
	go func() {
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var payload struct {
				Up   uint64 `json:"up"`
				Down uint64 `json:"down"`
			}
			if err := json.Unmarshal(line, &payload); err != nil {
				continue
			}
			select {
			case events <- streamEvent{up: payload.Up, down: payload.Down}:
			case <-streamContext.Done():
				return
			}
		}
		select {
		case events <- streamEvent{err: scanner.Err(), done: true}:
		case <-streamContext.Done():
		}
	}()
	idleTimeout := t.idleTimeout
	if idleTimeout <= 0 {
		idleTimeout = trafficHistoryIdle
	}
	idleTimer := time.NewTimer(idleTimeout)
	defer idleTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idleTimer.C:
			cancelStream()
			_ = response.Body.Close()
			return fmt.Errorf("Mihomo /traffic 超过 %s 未推送数据", idleTimeout)
		case event := <-events:
			if event.done {
				return event.err
			}
			t.Add(event.up, event.down)
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(idleTimeout)
		}
	}
}

func (t *trafficHistoryTracker) Run(ctx context.Context, client *mihomo.Client) {
	go t.flushLoop(ctx)
	var lastErrorLog time.Time
	for ctx.Err() == nil {
		if err := t.collectOnce(ctx, client); err != nil && ctx.Err() == nil {
			now := time.Now()
			if lastErrorLog.IsZero() || now.Sub(lastErrorLog) >= 30*time.Second {
				log.Printf("实时流量历史采集断开: %v", err)
				lastErrorLog = now
			}
		}
		retryDelay := t.retryDelay
		if retryDelay <= 0 {
			retryDelay = trafficHistoryRetry
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if err := t.Save(); err != nil {
		log.Printf("保存实时流量历史失败: %v", err)
	}
}

func (t *trafficHistoryTracker) flushLoop(ctx context.Context) {
	ticker := time.NewTicker(trafficHistoryFlush)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := t.Save(); err != nil {
				log.Printf("保存实时流量历史失败: %v", err)
			}
		}
	}
}
