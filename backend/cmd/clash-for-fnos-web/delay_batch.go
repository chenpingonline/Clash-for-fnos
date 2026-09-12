package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
)

const maxDelayBatchConcurrency = 10

type delayBatchRequest struct {
	Names []string `json:"names"`
}

type delayBatchResult struct {
	Name  string `json:"name"`
	Delay int    `json:"delay,omitempty"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type delayBatchJob struct {
	ID        string   `json:"jobId,omitempty"`
	State     string   `json:"state"`
	Names     []string `json:"names"`
	CreatedAt int64    `json:"createdAt,omitempty"`
	UpdatedAt int64    `json:"updatedAt,omitempty"`
}

type delayBatchStatus struct {
	delayBatchJob
	Results []delayBatchResult `json:"results"`
}

func normalizedDelayNames(names []string) ([]string, error) {
	if len(names) > 2000 {
		return nil, errors.New("单次测速节点不能超过 2000 个")
	}
	seen := make(map[string]struct{}, len(names))
	result := make([]string, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || len(name) > 512 || strings.ContainsRune(name, '\x00') {
			return nil, errors.New("测速节点名称无效")
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	if len(result) == 0 {
		return nil, errors.New("没有可测速节点")
	}
	return result, nil
}

func delayResult(ctx context.Context, client *mihomo.Client, name, testURL string, timeout int) delayBatchResult {
	query := url.Values{}
	query.Set("url", testURL)
	query.Set("timeout", strconv.Itoa(timeout))
	response, err := client.Do(ctx, http.MethodGet, "/proxies/"+url.PathEscape(name)+"/delay?"+query.Encode(), nil, time.Duration(timeout+3000)*time.Millisecond)
	if err != nil {
		state := "error"
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() || strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(err.Error(), "超时") {
			state = "timeout"
		}
		return delayBatchResult{Name: name, State: state, Error: err.Error()}
	}
	defer response.Body.Close()
	var payload struct {
		Delay int `json:"delay"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return delayBatchResult{Name: name, State: "error", Error: "解析测速结果失败: " + err.Error()}
	}
	if payload.Delay <= 0 {
		return delayBatchResult{Name: name, State: "error", Error: "测速未返回有效延迟"}
	}
	return delayBatchResult{Name: name, Delay: payload.Delay, State: "done"}
}

func (g *gateway) delayStatusLocked() delayBatchStatus {
	status := delayBatchStatus{delayBatchJob: delayBatchJob{State: "idle", Names: []string{}}, Results: make([]delayBatchResult, 0, len(g.delayResults))}
	if g.delayJob != nil {
		status.delayBatchJob = *g.delayJob
		status.Names = append([]string(nil), g.delayJob.Names...)
	}
	keys := make([]string, 0, len(g.delayResults))
	for name := range g.delayResults {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		status.Results = append(status.Results, g.delayResults[name])
	}
	return status
}

func (g *gateway) publishDelayStatusLocked() {
	status := g.delayStatusLocked()
	for updates := range g.delayWatchers {
		select {
		case updates <- status:
		default:
			select {
			case <-updates:
			default:
			}
			select {
			case updates <- status:
			default:
			}
		}
	}
}

func (g *gateway) startDelayBatch(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	body, ok := readLimitedBody(w, r, 1<<20)
	if !ok {
		return
	}
	var request delayBatchRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "测速请求格式错误"})
		return
	}
	names, err := normalizedDelayNames(request.Names)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	settings, err := client.LoadSettings()
	if err != nil {
		writeMihomoError(w, err)
		return
	}

	g.delayMu.Lock()
	if g.delayJob != nil && g.delayJob.State == "running" {
		status := g.delayStatusLocked()
		g.delayMu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"error": "已有测速任务正在进行", "job": status})
		return
	}
	now := time.Now().UnixMilli()
	g.delayJob = &delayBatchJob{ID: newHexID(8), State: "running", Names: names, CreatedAt: now, UpdatedAt: now}
	status := g.delayStatusLocked()
	g.publishDelayStatusLocked()
	g.delayMu.Unlock()

	go g.runDelayBatch(client, settings.HealthcheckURL, settings.HealthcheckTimeout, status.ID, names)
	writeJSON(w, http.StatusAccepted, status)
}

func (g *gateway) runDelayBatch(client *mihomo.Client, testURL string, timeout int, jobID string, names []string) {
	results := make(chan delayBatchResult, maxDelayBatchConcurrency)
	jobs := make(chan string)
	workerCount := min(maxDelayBatchConcurrency, len(names))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for name := range jobs {
				results <- delayResult(context.Background(), client, name, testURL, timeout)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, name := range names {
			jobs <- name
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	completed := make([]delayBatchResult, 0, len(names))
	for result := range results {
		completed = append(completed, result)
	}
	g.delayMu.Lock()
	defer g.delayMu.Unlock()
	if g.delayJob == nil || g.delayJob.ID != jobID {
		return
	}
	for _, result := range completed {
		g.delayResults[result.Name] = result
	}
	g.delayJob.State = "done"
	g.delayJob.UpdatedAt = time.Now().UnixMilli()
	g.publishDelayStatusLocked()
}

func (g *gateway) writeDelayStatus(w http.ResponseWriter) {
	g.delayMu.RLock()
	status := g.delayStatusLocked()
	g.delayMu.RUnlock()
	writeJSON(w, http.StatusOK, status)
}

func (g *gateway) streamDelayStatus(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "测速任务不存在或已过期"})
		return
	}
	g.delayMu.Lock()
	if g.delayJob == nil || g.delayJob.ID != id {
		g.delayMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "测速任务不存在或已过期"})
		return
	}
	initial := g.delayStatusLocked()
	updates := make(chan delayBatchStatus, 1)
	if initial.State == "running" {
		g.delayWatchers[updates] = struct{}{}
	}
	g.delayMu.Unlock()
	if initial.State == "running" {
		defer func() {
			g.delayMu.Lock()
			delete(g.delayWatchers, updates)
			g.delayMu.Unlock()
		}()
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "当前服务不支持流式响应"})
		return
	}
	writeEvent := func(value delayBatchStatus) bool {
		body, err := json.Marshal(value)
		if err != nil {
			return false
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !writeEvent(initial) || initial.State != "running" {
		return
	}
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case next := <-updates:
			if !writeEvent(next) || next.State != "running" {
				return
			}
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
