package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
)

func (g *gateway) geoStatus(ctx context.Context) (map[string]any, error) {
	status := map[string]any{}
	if err := g.helperJSON(ctx, http.MethodGet, "/geo/status", nil, &status, 10*time.Second); err != nil {
		return nil, err
	}
	return status, nil
}

func (g *gateway) writeGeoStatus(w http.ResponseWriter, r *http.Request) {
	status, err := g.geoStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (g *gateway) updateGeoData(w http.ResponseWriter, r *http.Request) {
	g.networkMu.Lock()
	defer g.networkMu.Unlock()
	status, err := g.geoStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if allowed, _ := status["canUpdate"].(bool); !allowed {
		message, _ := status["message"].(string)
		if message == "" {
			message = "当前运行模式不能更新 GEO 数据"
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": message})
		return
	}
	client := &mihomo.Client{SettingsFile: g.config.settingsFile}
	if err = mihomoRequest(r.Context(), client, http.MethodPost, "/upgrade/geo", bytes.NewBufferString("{}"), 3*time.Minute); err != nil {
		writeMihomoError(w, err)
		return
	}
	status, err = g.geoStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEO 数据已更新，但状态读取失败: " + err.Error()})
		return
	}
	status["updated"] = true
	writeJSON(w, http.StatusOK, status)
}

func (g *gateway) downloadGeoData(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Key string `json:"key"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	g.networkMu.Lock()
	defer g.networkMu.Unlock()
	var status map[string]any
	if err := g.helperJSON(r.Context(), http.MethodPost, "/geo/download", map[string]any{"key": input.Key}, &status, 3*time.Minute); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (g *gateway) updateGeoSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		AutoUpdate     bool `json:"autoUpdate"`
		UpdateInterval int  `json:"updateInterval"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	g.networkMu.Lock()
	defer g.networkMu.Unlock()
	previous, _, err := g.activeStartupConfig(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	var prepared map[string]any
	payload := map[string]any{"autoUpdate": input.AutoUpdate, "updateInterval": input.UpdateInterval}
	if err = g.helperJSON(r.Context(), http.MethodPost, "/geo/settings", payload, &prepared, time.Minute); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	txID := fmt.Sprint(prepared["txId"])
	rollback := func() {
		_ = g.helperJSON(context.Background(), http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, time.Minute)
		g.restoreRuntimeConfig(previous)
	}
	var activation map[string]any
	if err = g.helperJSON(r.Context(), http.MethodPost, "/config/activate", map[string]any{"txId": txID}, &activation, time.Minute); err != nil {
		rollback()
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEO 更新设置已回滚: " + err.Error()})
		return
	}
	effective, _ := prepared["effectiveContent"].(string)
	if effective == "" {
		rollback()
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEO 更新设置已回滚: 候选配置为空"})
		return
	}
	if err = g.applyConfig(r.Context(), []byte(effective)); err == nil {
		err = g.waitController(r.Context(), 30*time.Second)
	}
	if err != nil {
		rollback()
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEO 更新设置已回滚: " + err.Error()})
		return
	}
	_ = g.helperJSON(r.Context(), http.MethodPost, "/config/commit", map[string]any{"txId": txID}, nil, 10*time.Second)
	status, err := g.geoStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": prepared["settings"], "warning": "设置已生效，但状态读取失败: " + err.Error()})
		return
	}
	status["saved"] = true
	status["validation"] = prepared["validation"]
	status["activation"] = activation["method"]
	writeJSON(w, http.StatusOK, status)
}
