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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/privileged"
)

func (g *gateway) helperJSON(ctx context.Context, method, apiPath string, payload, target any, timeout time.Duration) error {
	return (privileged.Client{SocketPath: g.config.privilegedSocket}).DoJSON(ctx, method, apiPath, payload, target, timeout)
}

func (g *gateway) handleSystemAPI(w http.ResponseWriter, r *http.Request, requestPath string) bool {
	switch {
	case requestPath == "/api/app/icons" && r.Method == http.MethodGet:
		g.forwardHelper(w, r, http.MethodGet, "/app/icon/status", nil, 10*time.Second)
	case requestPath == "/api/app/icon" && r.Method == http.MethodPut:
		var body map[string]any
		if !decodeJSONBody(w, r, &body) {
			return true
		}
		g.forwardHelper(w, r, http.MethodPost, "/app/icon/update", body, 15*time.Second)
	case requestPath == "/api/app/update-info" && r.Method == http.MethodGet:
		g.writeAppUpdateStatus(w, r, false)
	case requestPath == "/api/app/check-update" && r.Method == http.MethodPost:
		g.writeAppUpdateStatus(w, r, true)
	case requestPath == "/api/network/settings" && r.Method == http.MethodGet:
		g.networkSettings(w, r)
	case requestPath == "/api/network/settings" && r.Method == http.MethodPut:
		g.updateNetworkSettings(w, r)
	case requestPath == "/api/network/tun" && r.Method == http.MethodPut:
		g.updateTun(w, r)
	case requestPath == "/api/system/status" && r.Method == http.MethodGet:
		g.writeCoreStatus(w, r, false)
	case requestPath == "/api/system/authorized-paths" && r.Method == http.MethodGet:
		writeJSON(w, 200, g.authorizedPathStatus())
	case requestPath == "/api/system/proxy-environment" && r.Method == http.MethodGet:
		g.proxyEnvironment(w, r, http.MethodGet, nil)
	case requestPath == "/api/system/proxy-environment" && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
		body := map[string]any{"enabled": false}
		if r.Method == http.MethodPut && !decodeJSONBody(w, r, &body) {
			return true
		}
		g.proxyEnvironment(w, r, http.MethodPost, body)
	case requestPath == "/api/core/bootstrap/retry" && r.Method == http.MethodPost:
		g.forwardHelperAndSync(w, r, "/bootstrap/retry", map[string]any{}, 3*time.Minute)
	case requestPath == "/api/core/mode" && r.Method == http.MethodPut:
		var body map[string]any
		if !decodeJSONBody(w, r, &body) {
			return true
		}
		g.forwardHelperAndSync(w, r, "/core/select-mode", body, 3*time.Minute)
	case requestPath == "/api/core/check-update" && r.Method == http.MethodPost:
		g.writeCoreStatus(w, r, true)
	case requestPath == "/api/core/update" && r.Method == http.MethodPost:
		var body struct{ Restart, Force bool }
		if !decodeJSONBody(w, r, &body) {
			return true
		}
		result, err := g.updateCore(r.Context(), body.Restart, body.Force)
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 200, result)
		}
	default:
		return false
	}
	return true
}

func patchRuntimeTun(ctx context.Context, client *mihomo.Client, enabled bool, timeout time.Duration) error {
	body, _ := json.Marshal(map[string]any{"tun": map[string]bool{"enable": enabled}})
	return mihomoRequest(ctx, client, http.MethodPatch, "/configs", strings.NewReader(string(body)), timeout)
}

func runtimeTunEnabled(ctx context.Context, client *mihomo.Client, timeout time.Duration) (bool, error) {
	response, err := client.Do(ctx, http.MethodGet, "/configs", nil, timeout)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	var payload struct {
		Tun struct {
			Enable *bool `json:"enable"`
		} `json:"tun"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return false, fmt.Errorf("读取 Mihomo TUN 状态失败: %w", err)
	}
	if payload.Tun.Enable == nil {
		return false, errors.New("Mihomo 未返回 TUN 运行状态")
	}
	return *payload.Tun.Enable, nil
}

func waitRuntimeTun(ctx context.Context, client *mihomo.Client, enabled bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		effective, err := runtimeTunEnabled(ctx, client, 2*time.Second)
		if err == nil && effective == enabled {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("期望 %t，实际 %t", enabled, effective)
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		timer := time.NewTimer(80 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func switchRuntimeTun(ctx context.Context, client *mihomo.Client, enabled bool) error {
	if err := patchRuntimeTun(ctx, client, enabled, 8*time.Second); err != nil {
		return err
	}
	return waitRuntimeTun(ctx, client, enabled, 1200*time.Millisecond)
}

func recentTunError(file string) string {
	handle, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer handle.Close()
	stat, err := handle.Stat()
	if err != nil {
		return ""
	}
	const tailLimit int64 = 256 << 10
	start := stat.Size() - tailLimit
	if start < 0 {
		start = 0
	}
	if _, err = handle.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(handle, tailLimit))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(body), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		lower := strings.ToLower(line)
		if strings.Contains(lower, "start tun listening error") || strings.Contains(lower, "tun adapter") && strings.Contains(lower, "error") {
			if len(line) > 500 {
				line = line[len(line)-500:]
			}
			return line
		}
	}
	return ""
}

func (g *gateway) rollbackTun(ctx context.Context, txID string, previous, restartManaged bool) {
	rollbackContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	client := &mihomo.Client{SettingsFile: g.config.settingsFile}
	if err := patchRuntimeTun(rollbackContext, client, previous, 10*time.Second); err != nil {
		log.Printf("TUN 快速切换运行态回滚失败: %v", err)
	}
	if err := g.helperJSON(rollbackContext, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 10*time.Second); err != nil {
		log.Printf("TUN 快速切换配置回滚失败: %v", err)
	}
	if restartManaged {
		if err := g.helperJSON(rollbackContext, http.MethodPost, "/core/restart-managed", map[string]any{}, nil, 20*time.Second); err != nil {
			log.Printf("TUN 快速切换 Core 回滚重启失败: %v", err)
		}
	}
}

func (g *gateway) updateTun(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "enabled 必须是布尔值"})
		return
	}

	g.networkMu.Lock()
	defer g.networkMu.Unlock()
	started := time.Now()
	stageStarted := started
	stages := map[string]int64{}
	result := "failed"
	stage := "prepare"
	defer func() {
		stageJSON, _ := json.Marshal(stages)
		log.Printf("TUN 快速切换结束 enabled=%t result=%s stage=%s duration=%dms stages=%s", *input.Enabled, result, stage, time.Since(started).Milliseconds(), stageJSON)
	}()
	markStage := func(name string) {
		now := time.Now()
		stages[name] = now.Sub(stageStarted).Milliseconds()
		stageStarted = now
	}

	stage = "prepare"
	var prepared map[string]any
	if err := g.helperJSON(r.Context(), http.MethodPost, "/network/tun", map[string]any{"enabled": *input.Enabled}, &prepared, 10*time.Second); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	markStage("prepare")
	txID, txOK := prepared["txId"].(string)
	previous, previousOK := prepared["previousEnabled"].(bool)
	if txID == "" || !txOK || !previousOK {
		if txID != "" {
			cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
			_ = g.helperJSON(cleanupContext, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 10*time.Second)
			cancel()
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "特权助手未返回完整的 TUN 配置事务"})
		return
	}

	client := &mihomo.Client{SettingsFile: g.config.settingsFile}
	stage = "runtime"
	err := switchRuntimeTun(r.Context(), client, *input.Enabled)
	runtimeMethod := "patch"
	persisted := false
	restartManagedFallback := false
	if err != nil && *input.Enabled {
		var status map[string]any
		statusErr := g.helperJSON(r.Context(), http.MethodGet, "/status", nil, &status, 3*time.Second)
		if statusErr == nil && status["mode"] == "managed" && status["canRestartService"] == true {
			stage = "persist-before-restart"
			var activation map[string]any
			if activateErr := g.helperJSON(r.Context(), http.MethodPost, "/config/activate", map[string]any{"txId": txID}, &activation, 10*time.Second); activateErr == nil {
				persisted = true
				markStage("persist-before-restart")
				stage = "restart-managed"
				restartManagedFallback = true
				if restartErr := g.helperJSON(r.Context(), http.MethodPost, "/core/restart-managed", map[string]any{}, nil, 20*time.Second); restartErr == nil {
					g.syncControllerSettings(r.Context())
					if readyErr := g.waitController(r.Context(), 12*time.Second); readyErr == nil {
						err = waitRuntimeTun(r.Context(), client, true, 1200*time.Millisecond)
					} else {
						err = readyErr
					}
					if err == nil {
						runtimeMethod = "managed-restart"
					}
				} else {
					err = restartErr
				}
			} else {
				err = activateErr
			}
		}
	}
	if err != nil {
		g.rollbackTun(r.Context(), txID, previous, restartManagedFallback)
		message := "TUN 运行态切换失败，已回滚: " + err.Error()
		if detail := recentTunError(g.config.mihomoLogFile); detail != "" {
			message += "；Mihomo: " + detail
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": message})
		return
	}
	markStage("runtime")

	stage = "persist"
	if !persisted {
		var activation map[string]any
		if err = g.helperJSON(r.Context(), http.MethodPost, "/config/activate", map[string]any{"txId": txID}, &activation, 10*time.Second); err != nil {
			g.rollbackTun(r.Context(), txID, previous, false)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "TUN 配置持久化失败，已回滚: " + err.Error()})
			return
		}
	}
	markStage("persist")
	stage = "commit"
	if err = g.helperJSON(r.Context(), http.MethodPost, "/config/commit", map[string]any{"txId": txID}, nil, 5*time.Second); err != nil {
		log.Printf("TUN 快速切换事务清理失败: %v", err)
	}
	markStage("commit")

	duration := time.Since(started).Milliseconds()
	result = "success"
	stage = "done"
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": *input.Enabled, "previousEnabled": previous, "activation": runtimeMethod, "attempts": 1, "durationMs": duration, "stages": stages})
}

func (g *gateway) forwardHelper(w http.ResponseWriter, r *http.Request, method, apiPath string, payload any, timeout time.Duration) {
	var result any
	if err := g.helperJSON(r.Context(), method, apiPath, payload, &result, timeout); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, result)
}

func (g *gateway) forwardHelperAndSync(w http.ResponseWriter, r *http.Request, apiPath string, payload any, timeout time.Duration) {
	var result any
	if err := g.helperJSON(r.Context(), http.MethodPost, apiPath, payload, &result, timeout); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	g.syncControllerSettings(r.Context())
	writeJSON(w, 200, result)
}

func defaultDNSSettings() map[string]any {
	return map[string]any{
		"enable": true, "listen": "127.0.0.1:1053", "enhancedMode": "fake-ip", "fakeIpRange": "198.18.0.1/16", "fakeIpRange6": "fdfe:dcba:9876::1/64", "fakeIpFilterMode": "blacklist",
		"ipv6": true, "preferH3": false, "respectRules": false, "useHosts": false, "useSystemHosts": false, "directNameserverFollowPolicy": false,
		"defaultNameserver": []string{"system", "223.6.6.6", "8.8.8.8", "2400:3200::1", "2001:4860:4860::8888"},
		"nameserver":        []string{"8.8.8.8", "https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"}, "fallback": []string{},
		"proxyServerNameserver": []string{"https://doh.pub/dns-query", "https://dns.alidns.com/dns-query", "tls://223.5.5.5"}, "directNameserver": []string{},
		"fakeIpFilter":     []string{"*.lan", "*.local", "*.arpa", "time.*.com", "ntp.*.com", "+.market.xiaomi.com", "localhost.ptlogin2.qq.com", "*.msftncsi.com", "www.msftconnecttest.com"},
		"nameserverPolicy": []any{}, "fallbackGeoip": true, "fallbackGeoipCode": "CN", "fallbackIpCidr": []string{"240.0.0.0/4", "0.0.0.0/32"}, "fallbackDomain": []string{"+.google.com", "+.facebook.com", "+.youtube.com"}, "hosts": []any{},
	}
}

func (g *gateway) dnsSettings() (bool, map[string]any, error) {
	enabled := false
	dns := defaultDNSSettings()
	err := g.settings.WithDocument(func(document map[string]any) error {
		enabled, _ = document["dnsOverrideEnabled"].(bool)
		if stored, ok := document["dnsOverrideSettings"].(map[string]any); ok {
			for key, value := range stored {
				dns[key] = value
			}
		}
		return nil
	})
	return enabled, dns, err
}

func (g *gateway) networkSettings(w http.ResponseWriter, r *http.Request) {
	var status map[string]any
	if err := g.helperJSON(r.Context(), http.MethodGet, "/network/status", nil, &status, 10*time.Second); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	enabled, dns, err := g.dnsSettings()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	settings, _ := status["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	settings["dnsOverrideEnabled"], settings["dns"] = enabled, dns
	status["settings"] = settings
	writeJSON(w, 200, status)
}

func (g *gateway) updateNetworkSettings(w http.ResponseWriter, r *http.Request) {
	var input map[string]any
	if !decodeJSONBody(w, r, &input) {
		return
	}
	g.networkMu.Lock()
	defer g.networkMu.Unlock()
	previousSettings, _ := os.ReadFile(g.config.settingsFile)
	currentEnabled, currentDNS, err := g.dnsSettings()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	nextEnabled := currentEnabled
	if value, ok := input["dnsOverrideEnabled"].(bool); ok {
		nextEnabled = value
	}
	nextDNS := currentDNS
	if value, ok := input["dns"].(map[string]any); ok {
		nextDNS = value
	}
	_, hasDNS := input["dns"]
	_, hasEnabled := input["dnsOverrideEnabled"]
	delete(input, "dns")
	delete(input, "dnsOverrideEnabled")
	if nextEnabled && (hasDNS || hasEnabled) {
		input["dns"] = nextDNS
	}
	if len(input) == 0 {
		err = g.saveDNSSettings(nextEnabled, nextDNS)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 200, map[string]any{"ok": true, "savedOnly": true, "settings": map[string]any{"dnsOverrideEnabled": nextEnabled, "dns": nextDNS}})
		}
		return
	}
	var prepared map[string]any
	if err = g.helperJSON(r.Context(), http.MethodPost, "/network/update", input, &prepared, time.Minute); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	txID := fmt.Sprint(prepared["txId"])
	var activation map[string]any
	if err = g.helperJSON(r.Context(), http.MethodPost, "/config/activate", map[string]any{"txId": txID}, &activation, time.Minute); err != nil {
		_ = g.helperJSON(r.Context(), http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, time.Minute)
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	if activation["method"] == "hot-reload" {
		if effective, ok := prepared["effectiveContent"].(string); ok {
			_ = g.applyConfig(r.Context(), []byte(effective))
		}
	}
	controller := ""
	if data, ok := prepared["controller"].(map[string]any); ok {
		controller, _ = data["clientUrl"].(string)
	}
	if controller != "" {
		err = g.settings.WithDocument(func(document map[string]any) error {
			document["controller"], document["controllerAutoDetect"] = strings.TrimRight(controller, "/"), true
			document["dnsOverrideEnabled"], document["dnsOverrideSettings"] = nextEnabled, nextDNS
			return nil
		})
	} else {
		err = g.saveDNSSettings(nextEnabled, nextDNS)
	}
	if err == nil {
		err = g.waitController(r.Context(), 30*time.Second)
	}
	if err != nil {
		_ = g.helperJSON(r.Context(), http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, time.Minute)
		if len(previousSettings) > 0 {
			_ = writeAtomicFile(g.config.settingsFile, previousSettings)
		}
		writeJSON(w, 502, map[string]string{"error": "网络设置已回滚: " + err.Error()})
		return
	}
	_ = g.helperJSON(r.Context(), http.MethodPost, "/config/commit", map[string]any{"txId": txID}, nil, 10*time.Second)
	var proxy any
	_ = g.helperJSON(r.Context(), http.MethodPost, "/system/proxy-environment/sync", map[string]any{}, &proxy, 30*time.Second)
	settings, _ := prepared["settings"].(map[string]any)
	if settings == nil {
		settings = map[string]any{}
	}
	settings["dnsOverrideEnabled"], settings["dns"] = nextEnabled, nextDNS
	writeJSON(w, 200, map[string]any{"ok": true, "settings": settings, "controller": controller, "configPath": prepared["target"], "backup": prepared["backup"], "validation": prepared["validation"], "activation": activation["method"], "warning": nil, "proxyEnvironment": proxy})
}

func (g *gateway) saveDNSSettings(enabled bool, dns map[string]any) error {
	return g.settings.WithDocument(func(document map[string]any) error {
		document["dnsOverrideEnabled"], document["dnsOverrideSettings"] = enabled, dns
		return nil
	})
}

func (g *gateway) authorizedPathStatus() map[string]any {
	items := []map[string]any{}
	for _, root := range g.authorizedRoots() {
		item := map[string]any{"path": root, "realPath": nil, "exists": false, "readable": false, "writable": false, "error": nil}
		if stat, err := os.Stat(root); err == nil && stat.IsDir() {
			item["exists"] = true
			if real, err := filepath.EvalSymlinks(root); err == nil {
				item["realPath"] = real
			}
			item["readable"] = true
			item["writable"] = syscall.Access(root, 2) == nil
		} else if err != nil {
			item["error"] = err.Error()
		}
		items = append(items, item)
	}
	return map[string]any{"enabled": len(items) > 0, "paths": items, "count": len(items), "processUid": os.Getuid(), "processGid": os.Getgid(), "runningAsRoot": os.Getuid() == 0}
}

func proxyEnv() []map[string]any {
	keys := []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}
	out := []map[string]any{}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			if parsed, err := url.Parse(value); err == nil && (parsed.User != nil) {
				parsed.User = url.User("***")
				value = parsed.String()
			}
			out = append(out, map[string]any{"key": key, "value": value})
		}
	}
	return out
}

func (g *gateway) proxyEnvironment(w http.ResponseWriter, r *http.Request, method string, payload any) {
	path := "/system/proxy-environment"
	if method == http.MethodPost {
		path = "/system/proxy-environment/update"
	}
	result := map[string]any{}
	if err := g.helperJSON(r.Context(), method, path, payload, &result, 30*time.Second); err != nil {
		if method == http.MethodGet {
			result = map[string]any{"ok": false, "error": err.Error(), "files": []any{}, "helperEnvironment": []any{}, "mihomoEnvironment": map[string]any{"pid": nil, "variables": []any{}}}
		} else {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
	}
	result["managerEnvironment"] = proxyEnv()
	writeJSON(w, 200, result)
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	Digest             string `json:"digest"`
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	PublishedAt any           `json:"published_at"`
	HTMLURL     any           `json:"html_url"`
	Assets      []githubAsset `json:"assets"`
}

func fetchRelease(ctx context.Context, repo string) (githubRelease, error) {
	var release githubRelease
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	req.Header.Set("User-Agent", "Clash-for-fnos/v"+version)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return release, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return release, fmt.Errorf("GitHub Release: HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&release); err != nil {
		return release, err
	}
	if release.TagName == "" {
		return release, errors.New("Release 未提供版本号")
	}
	return release, nil
}

func compareVersion(left, right string) int {
	parse := func(value string) [3]int {
		value = strings.TrimPrefix(value, "v")
		parts := strings.Split(value, ".")
		var out [3]int
		for i := 0; i < len(parts) && i < 3; i++ {
			out[i], _ = strconv.Atoi(strings.TrimFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' }))
		}
		return out
	}
	a, b := parse(left), parse(right)
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func platformName() string {
	if runtime.GOARCH == "arm64" || strings.HasPrefix(runtime.GOARCH, "arm") {
		return "arm"
	}
	return "x86"
}

func selectFPKAsset(release githubRelease) any {
	platform := platformName()
	for _, asset := range release.Assets {
		lower := strings.ToLower(asset.Name)
		if !strings.HasSuffix(lower, ".fpk") {
			continue
		}
		if (platform == "arm" && (strings.Contains(lower, "arm") || strings.Contains(lower, "aarch"))) || (platform == "x86" && (strings.Contains(lower, "x86") || strings.Contains(lower, "amd64"))) {
			return map[string]any{"name": asset.Name, "url": asset.BrowserDownloadURL, "size": asset.Size}
		}
	}
	return nil
}

func (g *gateway) appUpdateStatus(ctx context.Context, check bool) (map[string]any, error) {
	result := map[string]any{"appName": "Clash for fnOS", "currentVersion": version, "platform": platformName(), "sourceConfigured": g.config.releaseRepo != "", "releaseRepo": nullableString(g.config.releaseRepo), "updateAvailable": nil, "latest": nil}
	if !check || g.config.releaseRepo == "" {
		return result, nil
	}
	release, err := fetchRelease(ctx, g.config.releaseRepo)
	if err != nil {
		return nil, err
	}
	result["latest"] = map[string]any{"tag": release.TagName, "name": release.Name, "publishedAt": release.PublishedAt, "htmlUrl": release.HTMLURL, "asset": selectFPKAsset(release)}
	result["updateAvailable"] = compareVersion(release.TagName, version) > 0
	return result, nil
}
func (g *gateway) writeAppUpdateStatus(w http.ResponseWriter, r *http.Request, check bool) {
	result, err := g.appUpdateStatus(r.Context(), check)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
	} else {
		writeJSON(w, 200, result)
	}
}

func (g *gateway) coreStatus(ctx context.Context, latest bool) (map[string]any, error) {
	status := map[string]any{}
	if err := g.helperJSON(ctx, http.MethodGet, "/status", nil, &status, 10*time.Second); err != nil {
		return nil, err
	}
	delete(status, "managedSecret")
	delete(status, "detectedSecret")
	client := mihomo.Client{SettingsFile: g.config.settingsFile}
	var controllerVersion map[string]any
	if response, err := client.Do(ctx, http.MethodGet, "/version", nil, 8*time.Second); err == nil {
		_ = json.NewDecoder(response.Body).Decode(&controllerVersion)
		response.Body.Close()
	}
	current, _ := controllerVersion["version"].(string)
	if current == "" {
		current, _ = status["binaryVersion"].(string)
	}
	status["managerVersion"], status["currentVersion"], status["controllerVersion"] = version, nullableString(current), controllerVersion
	if latest {
		release, err := fetchRelease(ctx, "MetaCubeX/mihomo")
		if err != nil {
			return nil, err
		}
		asset, err := selectMihomoAsset(release)
		if err != nil {
			return nil, err
		}
		latestInfo := map[string]any{"tag": release.TagName, "name": release.Name, "publishedAt": release.PublishedAt, "htmlUrl": release.HTMLURL, "target": map[string]any{"os": "linux", "arch": runtime.GOARCH, "machine": runtime.GOARCH, "nodeArch": runtime.GOARCH}, "asset": asset}
		status["latest"], status["updateAvailable"] = latestInfo, nil
		if current != "" {
			status["updateAvailable"] = compareVersion(release.TagName, current) > 0
		}
	}
	return status, nil
}
func (g *gateway) writeCoreStatus(w http.ResponseWriter, r *http.Request, latest bool) {
	result, err := g.coreStatus(r.Context(), latest)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
	} else {
		writeJSON(w, 200, result)
	}
}

func selectMihomoAsset(release githubRelease) (map[string]any, error) {
	arch := runtime.GOARCH
	names := []string{}
	if arch == "amd64" {
		names = append(names, "mihomo-linux-amd64-v2-"+release.TagName+".gz", "mihomo-linux-amd64-"+release.TagName+".gz")
	} else {
		names = append(names, "mihomo-linux-"+arch+"-"+release.TagName+".gz")
	}
	for _, name := range names {
		for _, asset := range release.Assets {
			if asset.Name == name {
				return map[string]any{"name": asset.Name, "url": asset.BrowserDownloadURL, "size": asset.Size, "sha256": strings.TrimPrefix(asset.Digest, "sha256:")}, nil
			}
		}
	}
	return nil, fmt.Errorf("官方 Release 中没有找到适用于 %s 的 Mihomo Core", runtime.GOARCH)
}

func (g *gateway) updateCore(ctx context.Context, restart, force bool) (map[string]any, error) {
	before, err := g.coreStatus(ctx, false)
	if err != nil {
		return nil, err
	}
	release, err := fetchRelease(ctx, "MetaCubeX/mihomo")
	if err != nil {
		return nil, err
	}
	current, _ := before["currentVersion"].(string)
	if current != "" && compareVersion(release.TagName, current) <= 0 && !force {
		return map[string]any{"ok": true, "alreadyLatest": true, "before": before, "release": map[string]any{"tag": release.TagName}}, nil
	}
	asset, err := selectMihomoAsset(release)
	if err != nil {
		return nil, err
	}
	downloadURL, _ := asset["url"].(string)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	req.Header.Set("User-Agent", "Clash-for-fnos/v"+version)
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("下载 Mihomo Core: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (80<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 80<<20 {
		return nil, errors.New("Mihomo Core 超过大小限制")
	}
	if err = os.MkdirAll(g.config.coreStageDir, 0o700); err != nil {
		return nil, err
	}
	stage := filepath.Join(g.config.coreStageDir, fmt.Sprintf("%s.%d.download", filepath.Base(downloadURL), time.Now().UnixMilli()))
	if err = writeAtomicFile(stage, body); err != nil {
		return nil, err
	}
	defer os.Remove(stage)
	digest := sha256.Sum256(body)
	var install map[string]any
	if err = g.helperJSON(ctx, http.MethodPost, "/core/install", map[string]any{"stagePath": stage, "expectedVersion": release.TagName, "restart": restart}, &install, 2*time.Minute); err != nil {
		return nil, err
	}
	txID := fmt.Sprint(install["txId"])
	if restart {
		if restarted, _ := install["restarted"].(bool); !restarted {
			_ = g.helperJSON(ctx, http.MethodPost, "/core/rollback", map[string]any{"txId": txID, "restart": true}, nil, time.Minute)
			return nil, errors.New("内核重启失败，已尝试回滚")
		}
		if err = g.waitController(ctx, 30*time.Second); err != nil {
			_ = g.helperJSON(ctx, http.MethodPost, "/core/rollback", map[string]any{"txId": txID, "restart": true}, nil, time.Minute)
			return nil, err
		}
	}
	_ = g.helperJSON(ctx, http.MethodPost, "/core/commit", map[string]any{"txId": txID}, nil, 10*time.Second)
	return map[string]any{"ok": true, "before": before, "release": map[string]any{"tag": release.TagName, "asset": asset}, "stage": map[string]any{"compressedSha": hex.EncodeToString(digest[:]), "networkLabel": "直连"}, "install": install}, nil
}
