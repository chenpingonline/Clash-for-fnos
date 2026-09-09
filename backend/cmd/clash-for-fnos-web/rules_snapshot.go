package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
)

const (
	rulesSnapshotMaxBytes = 32 << 20
)

type rulesSnapshotStore struct {
	file       string
	refreshMu  sync.Mutex
	scheduleMu sync.Mutex
	timer      *time.Timer
}

func newRulesSnapshotStore(file string) *rulesSnapshotStore {
	return &rulesSnapshotStore{file: file}
}

func validRulesPayload(body []byte) bool {
	if len(body) == 0 || len(body) > rulesSnapshotMaxBytes || !json.Valid(body) {
		return false
	}
	var payload struct {
		Rules json.RawMessage `json:"rules"`
	}
	return json.Unmarshal(body, &payload) == nil && len(payload.Rules) > 0 && payload.Rules[0] == '['
}

func (s *rulesSnapshotStore) load() ([]byte, error) {
	if s == nil || s.file == "" {
		return nil, os.ErrNotExist
	}
	body, err := os.ReadFile(s.file)
	if err != nil {
		return nil, err
	}
	if !validRulesPayload(body) {
		return nil, errors.New("规则快照无效")
	}
	return body, nil
}

func (s *rulesSnapshotStore) save(body []byte) error {
	if s == nil || s.file == "" {
		return nil
	}
	if !validRulesPayload(body) {
		return errors.New("Mihomo 返回的规则数据无效或过大")
	}
	return writeAtomicFile(s.file, body)
}

func (s *rulesSnapshotStore) refresh(ctx context.Context, client *mihomo.Client) ([]byte, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	response, err := client.Do(ctx, http.MethodGet, "/rules", nil, 12*time.Second)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, rulesSnapshotMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if !validRulesPayload(body) {
		return nil, errors.New("Mihomo 返回的规则数据无效或过大")
	}
	if err := s.save(body); err != nil {
		return nil, fmt.Errorf("保存规则快照失败: %w", err)
	}
	return body, nil
}

func (s *rulesSnapshotStore) scheduleRefresh(settingsFile string, delay time.Duration) {
	if s == nil || s.file == "" {
		return
	}
	s.scheduleMu.Lock()
	defer s.scheduleMu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(delay, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := s.refresh(ctx, &mihomo.Client{SettingsFile: settingsFile}); err != nil {
			log.Printf("刷新规则快照失败: %v", err)
		}
	})
}

func (g *gateway) rulesChanged() {
	if g.rulesSnapshot == nil {
		return
	}
	g.rulesSnapshot.scheduleRefresh(g.config.settingsFile, 500*time.Millisecond)
}

func (g *gateway) writeRules(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	refresh := r.URL.Query().Get("refresh") == "1"
	if !refresh {
		if body, err := g.rulesSnapshot.load(); err == nil {
			writeRawJSON(w, http.StatusOK, body, "disk")
			return
		}
	}
	body, err := g.rulesSnapshot.refresh(r.Context(), client)
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	writeRawJSON(w, http.StatusOK, body, "mihomo")
}

func contentETag(body []byte) string {
	digest := sha256.Sum256(body)
	return `"` + hex.EncodeToString(digest[:12]) + `"`
}

func writeRawJSON(w http.ResponseWriter, status int, body []byte, source string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if source != "" {
		w.Header().Set("X-Clash-Data-Source", source)
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeConditionalJSON(w http.ResponseWriter, r *http.Request, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "编码响应失败"})
		return
	}
	etag := contentETag(body)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
