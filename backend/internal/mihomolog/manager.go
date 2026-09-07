package mihomolog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
)

const MaxBytes int64 = 1024 * 1024
const trimTarget int64 = 768 * 1024

type Record struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type subscriber struct {
	level   string
	channel chan Record
}

type Manager struct {
	File   string
	mu     sync.Mutex
	subsMu sync.Mutex
	subs   map[*subscriber]struct{}
}

func New(file string) *Manager { return &Manager{File: file, subs: map[*subscriber]struct{}{}} }

func normalizeLevel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "warn" {
		return "warning"
	}
	switch value {
	case "debug", "info", "warning", "error":
		return value
	}
	return "info"
}
func rank(value string) int {
	return map[string]int{"debug": 10, "info": 20, "warning": 30, "error": 40}[normalizeLevel(value)]
}

func Normalize(line []byte) (Record, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Record{}, false
	}
	var raw map[string]any
	if json.Unmarshal(line, &raw) != nil {
		raw = map[string]any{"payload": string(line)}
	}
	text := func(key string) string {
		if value, ok := raw[key]; ok && value != nil {
			return fmt.Sprint(value)
		}
		return ""
	}
	when := text("time")
	if when == "" {
		when = time.Now().UTC().Format(time.RFC3339Nano)
	}
	level := text("level")
	if level == "" {
		level = text("type")
	}
	message := text("message")
	if message == "" {
		message = text("payload")
	}
	if message == "" {
		message = string(line)
	}
	return Record{Time: when, Level: normalizeLevel(level), Message: message}, true
}

func (m *Manager) Append(record Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(m.File), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(m.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(record)
	_, writeErr := file.Write(append(body, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if info, err := os.Stat(m.File); err == nil && info.Size() > MaxBytes {
		return m.trim(info.Size())
	}
	return nil
}

func (m *Manager) trim(size int64) error {
	file, err := os.Open(m.File)
	if err != nil {
		return err
	}
	defer file.Close()
	keep := trimTarget
	if size < keep {
		keep = size
	}
	buffer := make([]byte, keep)
	if _, err := file.ReadAt(buffer, size-keep); err != nil && err != io.EOF {
		return err
	}
	if index := bytes.IndexByte(buffer, '\n'); index >= 0 {
		buffer = buffer[index+1:]
	}
	return os.WriteFile(m.File, buffer, 0o600)
}

func (m *Manager) History(level string, limit int) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit < 1 {
		limit = 800
	}
	if limit > 2000 {
		limit = 2000
	}
	body, err := os.ReadFile(m.File)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	items := []Record{}
	selected := rank(level)
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		var record Record
		if json.Unmarshal(line, &record) == nil && rank(record.Level) >= selected {
			items = append(items, record)
		}
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return map[string]any{"items": items, "size": len(body), "maxBytes": MaxBytes}, nil
}

func (m *Manager) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(m.File), 0o700); err != nil {
		return err
	}
	return os.WriteFile(m.File, nil, 0o600)
}

func (m *Manager) broadcast(record Record) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for item := range m.subs {
		if rank(record.Level) >= rank(item.level) {
			select {
			case item.channel <- record:
			default:
			}
		}
	}
}

func (m *Manager) ServeSSE(w http.ResponseWriter, r *http.Request, level string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	item := &subscriber{level: normalizeLevel(level), channel: make(chan Record, 64)}
	m.subsMu.Lock()
	m.subs[item] = struct{}{}
	m.subsMu.Unlock()
	defer func() { m.subsMu.Lock(); delete(m.subs, item); m.subsMu.Unlock() }()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case record := <-item.channel:
			body, _ := json.Marshal(record)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", body)
			flusher.Flush()
		case <-ticker.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (m *Manager) Run(ctx context.Context, settingsFile string) {
	for ctx.Err() == nil {
		client := &mihomo.Client{SettingsFile: settingsFile}
		streamContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		response, err := client.Do(streamContext, http.MethodGet, "/logs?level=debug&format=structured", nil, 0)
		if err == nil {
			scanner := bufio.NewScanner(response.Body)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for scanner.Scan() {
				record, ok := Normalize(scanner.Bytes())
				if ok {
					if m.Append(record) == nil {
						m.broadcast(record)
					}
				}
			}
			response.Body.Close()
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(1500 * time.Millisecond):
		}
	}
}

func ParseLimit(value string) int {
	number, err := strconv.Atoi(value)
	if err != nil {
		return 800
	}
	return number
}
