package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
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

func (g *gateway) streamDelayBatch(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
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

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	results := make(chan delayBatchResult, maxDelayBatchConcurrency)
	jobs := make(chan string)
	workerCount := min(maxDelayBatchConcurrency, len(names))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for name := range jobs {
				result := delayResult(r.Context(), client, name, settings.HealthcheckURL, settings.HealthcheckTimeout)
				select {
				case results <- result:
				case <-r.Context().Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, name := range names {
			select {
			case jobs <- name:
			case <-r.Context().Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	encoder := json.NewEncoder(w)
	for result := range results {
		if encoder.Encode(result) != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}
