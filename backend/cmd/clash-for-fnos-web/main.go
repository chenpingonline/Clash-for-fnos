package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/appsettings"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/configyaml"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomolog"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/privileged"
)

const appName = "clash-for-fnos"

var version = "dev"

type config struct {
	socketPath         string
	publicDir          string
	gateway            string
	settingsFile       string
	selectedFile       string
	managedConfigFile  string
	privilegedSocket   string
	mihomoLogFile      string
	configMetaFile     string
	backupDir          string
	profilesFile       string
	profileDir         string
	authorizedFile     string
	accessiblePaths    string
	coreStageDir       string
	trafficTotalsFile  string
	trafficHistoryFile string
	rulesSnapshotFile  string
	exitLocationURL    string
	releaseRepo        string
}

type gateway struct {
	config         config
	selectionMu    sync.Mutex
	ruleProviderMu sync.Mutex
	configMu       sync.Mutex
	networkMu      sync.Mutex
	profileMu      sync.Mutex
	jobMu          sync.Mutex
	localScanMu    sync.Mutex
	tunOperationMu sync.RWMutex
	profileJobs    map[string]*profileJob
	activeJobs     map[string]string
	localScans     map[string]localCandidate
	logs           *mihomolog.Manager
	settings       *appsettings.Store
	trafficTotals  *trafficTotalsTracker
	trafficHistory *trafficHistoryTracker
	rulesSnapshot  *rulesSnapshotStore
	tunOperation   tunOperationStatus
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func loadConfig() config {
	return config{
		socketPath:         env("SOCKET_PATH", "/tmp/clash-for-fnos.sock"),
		publicDir:          env("PUBLIC_DIR", "./public"),
		gateway:            strings.TrimSuffix(env("GATEWAY_PREFIX", "/app/"+appName), "/"),
		settingsFile:       filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "settings.json"),
		selectedFile:       filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "selected.json"),
		managedConfigFile:  filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "config.yaml"),
		privilegedSocket:   env("PRIV_SOCKET_PATH", "/tmp/clash-for-fnos-priv.sock"),
		mihomoLogFile:      filepath.Join(env("TRIM_PKGVAR", "/tmp/clash-for-fnos-var"), "mihomo.log"),
		configMetaFile:     filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "config-meta.json"),
		backupDir:          filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "backups"),
		profilesFile:       filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "profiles.json"),
		profileDir:         filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "profiles"),
		authorizedFile:     filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "authorized-paths.txt"),
		accessiblePaths:    os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS"),
		coreStageDir:       filepath.Join(env("TRIM_PKGVAR", "/tmp/clash-for-fnos-var"), "core-stage"),
		trafficTotalsFile:  filepath.Join(env("TRIM_PKGVAR", "/tmp/clash-for-fnos-var"), "traffic-totals.json"),
		trafficHistoryFile: filepath.Join(env("TRIM_PKGVAR", "/tmp/clash-for-fnos-var"), "traffic-history.json"),
		rulesSnapshotFile:  filepath.Join(env("TRIM_PKGVAR", "/tmp/clash-for-fnos-var"), "rules-snapshot.json"),
		exitLocationURL:    env("CLASH_EXIT_LOCATION_URL", "https://ipwho.is/?lang=zh-CN&fields=success,message,ip,country,country_code,region,city,timezone"),
		releaseRepo:        env("CLASH_FOR_FNOS_RELEASE_REPO", "chenpingonline/Clash-for-fnos"),
	}
}

func newGateway(cfg config) *gateway {
	return &gateway{
		config:         cfg,
		logs:           mihomolog.New(cfg.mihomoLogFile),
		settings:       &appsettings.Store{File: cfg.settingsFile},
		profileJobs:    make(map[string]*profileJob),
		activeJobs:     make(map[string]string),
		localScans:     make(map[string]localCandidate),
		trafficTotals:  newTrafficTotalsTracker(cfg.trafficTotalsFile),
		trafficHistory: newTrafficHistoryTracker(cfg.trafficHistoryFile),
		rulesSnapshot:  newRulesSnapshotStore(cfg.rulesSnapshotFile),
	}
}

func stripPrefix(requestPath, prefix string) string {
	if requestPath == prefix {
		return "/"
	}
	if strings.HasPrefix(requestPath, prefix+"/") {
		return strings.TrimPrefix(requestPath, prefix)
	}
	return requestPath
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == g.config.gateway {
		target := g.config.gateway + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}

	requestPath := stripPrefix(r.URL.Path, g.config.gateway)
	if requestPath == "/api/health" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "app": appName, "version": version,
			"backend": "go",
		})
		return
	}
	if g.handleLogs(w, r, requestPath) {
		return
	}
	if g.handleSettings(w, r, requestPath) {
		return
	}
	if g.handleConfigAPI(w, r, requestPath) {
		return
	}
	if g.handleProfilesAPI(w, r, requestPath) {
		return
	}
	if g.handleSystemAPI(w, r, requestPath) {
		return
	}
	if g.handleMihomoAPI(w, r, requestPath) {
		return
	}
	if strings.HasPrefix(requestPath, "/api/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
		return
	}
	if g.serveStatic(w, r, requestPath) {
		return
	}
	if !strings.HasPrefix(requestPath, "/api/") && g.serveStatic(w, r, "/") {
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
}

func (g *gateway) handleConfigAPI(w http.ResponseWriter, r *http.Request, requestPath string) bool {
	switch {
	case requestPath == "/api/config/meta" && r.Method == http.MethodGet:
		payload := map[string]any{"source": "managed", "path": nil, "importedAt": nil, "appliedAt": nil}
		if body, err := os.ReadFile(g.config.configMetaFile); err == nil {
			_ = json.Unmarshal(body, &payload)
		}
		writeJSON(w, 200, payload)
	case requestPath == "/api/config/effective" && r.Method == http.MethodGet:
		var payload any
		if err := (privileged.Client{SocketPath: g.config.privilegedSocket}).GetJSON(r.Context(), "/config/active-raw", &payload); err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
		} else {
			writeConditionalJSON(w, r, payload)
		}
	case requestPath == "/api/config/raw" && r.Method == http.MethodGet:
		body, err := os.ReadFile(g.config.managedConfigFile)
		if err != nil {
			body = []byte("# 在这里粘贴完整的 Mihomo YAML 配置\n")
		}
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		_, _ = w.Write(body)
	case requestPath == "/api/config/backups" && r.Method == http.MethodGet:
		entries, _ := os.ReadDir(g.config.backupDir)
		items := []string{}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
				items = append(items, entry.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(items)))
		writeJSON(w, 200, map[string]any{"items": items})
	case requestPath == "/api/config/raw" && r.Method == http.MethodPut:
		raw, ok := readTextBody(w, r, 12<<20)
		if !ok {
			return true
		}
		g.configMu.Lock()
		err := g.saveAndApplyConfig(r.Context(), raw)
		g.configMu.Unlock()
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
	case requestPath == "/api/config/apply" && r.Method == http.MethodPost:
		raw, err := os.ReadFile(g.config.managedConfigFile)
		if err == nil {
			err = g.applyConfig(r.Context(), raw)
		}
		if err == nil {
			g.rulesChanged()
			go func() { time.Sleep(1200 * time.Millisecond); g.restoreSelections(context.Background()) }()
		}
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
	case requestPath == "/api/config/sync-startup" && r.Method == http.MethodPost:
		raw, err := os.ReadFile(g.config.managedConfigFile)
		if err == nil {
			g.configMu.Lock()
			var result map[string]any
			result, err = g.syncStartupConfig(r.Context(), raw)
			g.configMu.Unlock()
			if err == nil {
				result["ok"] = true
				writeJSON(w, 200, result)
				return true
			}
		}
		writeJSON(w, 502, map[string]string{"error": err.Error()})
	case strings.HasPrefix(requestPath, "/api/config/backups/") && r.Method == http.MethodPost:
		name, err := url.PathUnescape(strings.TrimPrefix(requestPath, "/api/config/backups/"))
		if err != nil || !validBackupName(name) {
			writeJSON(w, 400, map[string]string{"error": "备份文件名非法"})
			return true
		}
		raw, err := os.ReadFile(filepath.Join(g.config.backupDir, name))
		if err == nil {
			g.configMu.Lock()
			err = g.saveAndApplyConfig(r.Context(), raw)
			g.configMu.Unlock()
		}
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
	default:
		return false
	}
	return true
}

func readTextBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return nil, false
	}
	if int64(len(body)) > limit {
		writeJSON(w, 413, map[string]string{"error": "请求体过大"})
		return nil, false
	}
	if len(bytes.TrimSpace(body)) == 0 || bytes.IndexByte(body, 0) >= 0 {
		writeJSON(w, 400, map[string]string{"error": "配置内容无效"})
		return nil, false
	}
	return body, true
}
func validBackupName(name string) bool {
	if !strings.HasSuffix(name, ".yaml") {
		return false
	}
	for _, char := range strings.TrimSuffix(name, ".yaml") {
		if (char < '0' || char > '9') && char != 'T' && char != '-' && char != '.' {
			return false
		}
	}
	return true
}
func writeAtomicFile(file string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.%d.tmp", file, os.Getpid())
	if err := os.WriteFile(temporary, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, file); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
func (g *gateway) backupConfig() error {
	old, err := os.ReadFile(g.config.managedConfigFile)
	if err != nil {
		return nil
	}
	if err := os.MkdirAll(g.config.backupDir, 0o700); err != nil {
		return err
	}
	name := time.Now().UTC().Format("2006-01-02T15-04-05.000Z") + ".yaml"
	if err := os.WriteFile(filepath.Join(g.config.backupDir, name), old, 0o600); err != nil {
		return err
	}
	entries, _ := os.ReadDir(g.config.backupDir)
	names := []string{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".yaml") {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names[minimum(20, len(names)):] {
		_ = os.Remove(filepath.Join(g.config.backupDir, name))
	}
	return nil
}
func minimum(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func (g *gateway) applyConfig(ctx context.Context, raw []byte) error {
	client := &mihomo.Client{SettingsFile: g.config.settingsFile}
	body, _ := json.Marshal(map[string]any{"path": "", "payload": string(raw)})
	return mihomoRequest(ctx, client, http.MethodPut, "/configs?force=true", bytes.NewReader(body), 120*time.Second)
}

func sameConfigFile(file string, raw []byte) bool {
	current, err := os.ReadFile(file)
	return err == nil && bytes.Equal(current, raw)
}

func restoreFileSnapshot(file string, raw []byte, existed bool) {
	if existed {
		if err := writeAtomicFile(file, raw); err != nil {
			log.Printf("恢复文件快照失败 file=%s err=%v", file, err)
		}
		return
	}
	if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("清理事务中新建文件失败 file=%s err=%v", file, err)
	}
}

func (g *gateway) activeStartupConfig(ctx context.Context) ([]byte, map[string]any, error) {
	payload := map[string]any{}
	err := (privileged.Client{SocketPath: g.config.privilegedSocket}).DoJSON(ctx, http.MethodGet, "/config/active-raw", nil, &payload, 3*time.Second)
	if err != nil {
		return nil, nil, err
	}
	content, _ := payload["content"].(string)
	return []byte(content), payload, nil
}

func (g *gateway) restoreRuntimeConfig(raw []byte) {
	if len(raw) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := g.applyConfig(ctx, raw); err != nil {
		log.Printf("配置运行态回滚失败: %v", err)
	}
}
func (g *gateway) waitController(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &mihomo.Client{SettingsFile: g.config.settingsFile}
	var last error
	for time.Now().Before(deadline) {
		response, err := client.Do(ctx, http.MethodGet, "/version", nil, 3500*time.Millisecond)
		if err == nil {
			response.Body.Close()
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("Mihomo Controller 未在限定时间内恢复: %w", last)
}
func (g *gateway) saveAndApplyConfig(ctx context.Context, raw []byte) error {
	started := time.Now()
	previous, previousErr := os.ReadFile(g.config.managedConfigFile)
	applyStarted := time.Now()
	if err := g.applyConfig(ctx, raw); err != nil {
		return err
	}
	applyDuration := time.Since(applyStarted).Milliseconds()
	if err := g.backupConfig(); err != nil {
		g.restoreRuntimeConfig(previous)
		return err
	}
	if err := writeAtomicFile(g.config.managedConfigFile, raw); err != nil {
		g.restoreRuntimeConfig(previous)
		return err
	}
	meta := map[string]any{}
	if body, err := os.ReadFile(g.config.configMetaFile); err == nil {
		_ = json.Unmarshal(body, &meta)
	}
	meta["active"] = true
	meta["savedAt"] = time.Now().UnixMilli()
	meta["appliedAt"] = time.Now().UnixMilli()
	if _, ok := meta["source"]; !ok {
		meta["source"] = "managed"
	}
	body, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeAtomicFile(g.config.configMetaFile, body); err != nil {
		restoreFileSnapshot(g.config.managedConfigFile, previous, previousErr == nil)
		g.restoreRuntimeConfig(previous)
		return err
	}
	log.Printf("配置应用完成 result=applied duration=%dms stages=apply:%dms", time.Since(started).Milliseconds(), applyDuration)
	g.rulesChanged()
	return nil
}
func (g *gateway) syncStartupConfig(ctx context.Context, raw []byte) (map[string]any, error) {
	return g.syncStartupConfigWithStage(ctx, raw, nil)
}

func (g *gateway) syncStartupConfigWithStage(ctx context.Context, raw []byte, stage func(string, string)) (map[string]any, error) {
	progress := func(name, message string) {
		if stage != nil {
			stage(name, message)
		}
	}
	started := time.Now()
	stages := map[string]int64{}
	helper := privileged.Client{SocketPath: g.config.privilegedSocket}
	progress("inspect", "正在检查当前启动配置…")
	activeStarted := time.Now()
	activeRaw, active, activeErr := g.activeStartupConfig(ctx)
	stages["inspect"] = time.Since(activeStarted).Milliseconds()
	if activeErr == nil && bytes.Equal(activeRaw, raw) && sameConfigFile(g.config.managedConfigFile, raw) {
		result := map[string]any{"target": active["path"], "validation": map[string]any{"ok": true, "method": "unchanged", "skipped": true}, "activation": map[string]any{"method": "unchanged"}, "unchanged": true, "durationMs": time.Since(started).Milliseconds(), "stages": stages}
		log.Printf("启动配置同步跳过 result=unchanged duration=%dms", time.Since(started).Milliseconds())
		return result, nil
	}
	previous, previousErr := os.ReadFile(g.config.managedConfigFile)
	if previousErr != nil && activeErr == nil {
		previous = activeRaw
	}
	rollbackRuntime := func() { g.restoreRuntimeConfig(previous) }
	var syncResult map[string]any
	progress("validate", "正在备份并使用 Mihomo 校验配置…")
	prepareStarted := time.Now()
	if err := helper.DoJSON(ctx, http.MethodPost, "/config/sync", map[string]any{"content": string(raw)}, &syncResult, 60*time.Second); err != nil {
		return nil, fmt.Errorf("备份或校验启动配置失败: %w", err)
	}
	stages["validate"] = time.Since(prepareStarted).Milliseconds()
	txID, _ := syncResult["txId"].(string)
	var activation map[string]any
	progress("activate", "配置校验通过，正在写入启动配置…")
	activateStarted := time.Now()
	if err := helper.DoJSON(ctx, http.MethodPost, "/config/activate", map[string]any{"txId": txID}, &activation, 60*time.Second); err != nil {
		_ = helper.DoJSON(ctx, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 60*time.Second)
		return nil, fmt.Errorf("写入启动配置失败: %w", err)
	}
	stages["activate"] = time.Since(activateStarted).Milliseconds()
	effective := raw
	if value, ok := syncResult["effectiveContent"].(string); ok {
		effective = []byte(value)
	}
	if activation["method"] == "hot-reload" {
		progress("apply", "正在应用运行配置…")
		applyStarted := time.Now()
		if err := g.applyConfig(ctx, effective); err != nil {
			_ = helper.DoJSON(ctx, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 60*time.Second)
			rollbackRuntime()
			return nil, fmt.Errorf("应用运行配置失败: %w", err)
		}
		stages["apply"] = time.Since(applyStarted).Milliseconds()
	}
	progress("controller", "正在等待 Mihomo Controller 恢复…")
	readyStarted := time.Now()
	if err := g.waitController(ctx, 30*time.Second); err != nil {
		_ = helper.DoJSON(ctx, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 60*time.Second)
		rollbackRuntime()
		return nil, fmt.Errorf("确认 Controller 状态失败: %w", err)
	}
	stages["controllerReady"] = time.Since(readyStarted).Milliseconds()
	progress("persist", "Controller 已恢复，正在保存配置与元数据…")
	persistStarted := time.Now()
	if err := g.backupConfig(); err != nil {
		_ = helper.DoJSON(ctx, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 60*time.Second)
		rollbackRuntime()
		return nil, fmt.Errorf("备份托管配置失败: %w", err)
	}
	if err := writeAtomicFile(g.config.managedConfigFile, raw); err != nil {
		_ = helper.DoJSON(ctx, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 60*time.Second)
		rollbackRuntime()
		return nil, fmt.Errorf("保存托管配置失败: %w", err)
	}
	meta := map[string]any{}
	if body, err := os.ReadFile(g.config.configMetaFile); err == nil {
		_ = json.Unmarshal(body, &meta)
	}
	now := time.Now().UnixMilli()
	if _, ok := meta["source"]; !ok {
		meta["source"] = "managed"
	}
	meta["active"] = true
	meta["appliedAt"] = now
	meta["startupSyncedAt"] = now
	meta["startupConfigPath"] = syncResult["target"]
	meta["startupBackupPath"] = syncResult["backup"]
	meta["activationMethod"] = activation["method"]
	delete(meta, "reloadWarning")
	metaBody, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		_ = helper.DoJSON(ctx, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 60*time.Second)
		restoreFileSnapshot(g.config.managedConfigFile, previous, previousErr == nil)
		rollbackRuntime()
		return nil, fmt.Errorf("编码配置元数据失败: %w", err)
	}
	if err := writeAtomicFile(g.config.configMetaFile, metaBody); err != nil {
		_ = helper.DoJSON(ctx, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 60*time.Second)
		restoreFileSnapshot(g.config.managedConfigFile, previous, previousErr == nil)
		rollbackRuntime()
		return nil, fmt.Errorf("保存配置元数据失败: %w", err)
	}
	stages["persist"] = time.Since(persistStarted).Milliseconds()
	progress("commit", "正在提交安全事务并清理临时文件…")
	commitStarted := time.Now()
	_ = helper.DoJSON(ctx, http.MethodPost, "/config/commit", map[string]any{"txId": txID}, nil, 10*time.Second)
	stages["commit"] = time.Since(commitStarted).Milliseconds()
	go func() { time.Sleep(1200 * time.Millisecond); g.restoreSelections(context.Background()) }()
	duration := time.Since(started).Milliseconds()
	log.Printf("启动配置同步完成 result=applied duration=%dms stages=%v", duration, stages)
	g.rulesChanged()
	return map[string]any{"target": syncResult["target"], "backup": syncResult["backup"], "validation": syncResult["validation"], "activation": activation, "unchanged": false, "durationMs": duration, "stages": stages}, nil
}

func (g *gateway) handleSettings(w http.ResponseWriter, r *http.Request, requestPath string) bool {
	if requestPath == "/api/settings" && r.Method == http.MethodGet {
		payload, err := g.settings.ReadPublic()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 200, payload)
		}
		return true
	}
	if requestPath != "/api/settings" || r.Method != http.MethodPut {
		return false
	}
	body, ok := readLimitedBody(w, r, 1<<20)
	if !ok {
		return true
	}
	var update appsettings.Update
	if err := json.Unmarshal(body, &update); err != nil {
		writeJSON(w, 400, map[string]string{"error": "JSON 格式错误"})
		return true
	}
	err := g.settings.WithDocument(func(document map[string]any) error {
		if err := appsettings.Apply(document, update); err != nil {
			return err
		}
		if auto, ok := document["controllerAutoDetect"].(bool); !ok || auto {
			var status map[string]any
			if err := (privileged.Client{SocketPath: g.config.privilegedSocket}).GetJSON(r.Context(), "/status", &status); err == nil {
				appsettings.SyncController(document, status)
			}
		}
		return nil
	})
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return true
	}
	payload, err := g.settings.ReadPublic()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
	} else {
		writeJSON(w, 200, payload)
	}
	return true
}

func (g *gateway) syncControllerSettings(ctx context.Context) {
	var status map[string]any
	if err := (privileged.Client{SocketPath: g.config.privilegedSocket}).GetJSON(ctx, "/status", &status); err != nil {
		return
	}
	_ = g.settings.WithDocument(func(document map[string]any) error { appsettings.SyncController(document, status); return nil })
}

func (g *gateway) restoreSelections(ctx context.Context) {
	client := &mihomo.Client{SettingsFile: g.config.settingsFile}
	settings, err := client.LoadSettings()
	if err != nil || !settings.PersistSelections {
		return
	}
	body, err := os.ReadFile(g.config.selectedFile)
	if err != nil {
		return
	}
	selected := map[string]string{}
	if json.Unmarshal(body, &selected) != nil {
		return
	}
	payload, err := mihomoJSON(ctx, client, "/proxies")
	if err != nil {
		return
	}
	all, _ := payload["proxies"].(map[string]any)
	for group, node := range selected {
		raw, ok := all[group].(map[string]any)
		if !ok {
			continue
		}
		nodes, _ := raw["all"].([]any)
		valid := false
		for _, candidate := range nodes {
			if fmt.Sprint(candidate) == node {
				valid = true
				break
			}
		}
		if !valid {
			continue
		}
		encodedGroup := url.PathEscape(group)
		requestBody, _ := json.Marshal(map[string]string{"name": node})
		_ = mihomoRequest(ctx, client, http.MethodPut, "/proxies/"+encodedGroup, bytes.NewReader(requestBody), 12*time.Second)
	}
}

func (g *gateway) runStartupTasks(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Second):
		g.syncControllerSettings(ctx)
	}
	g.reconcileManagedTunAfterStartup(ctx)
	select {
	case <-ctx.Done():
		return
	case <-time.After(4 * time.Second):
		g.restoreSelections(ctx)
	}
}

func (g *gateway) handleLogs(w http.ResponseWriter, r *http.Request, requestPath string) bool {
	switch {
	case requestPath == "/api/logs/history" && r.Method == http.MethodGet:
		payload, err := g.logs.History(r.URL.Query().Get("level"), mihomolog.ParseLimit(r.URL.Query().Get("limit")))
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "读取日志失败: " + err.Error()})
		} else {
			writeJSON(w, 200, payload)
		}
	case requestPath == "/api/logs/history" && r.Method == http.MethodDelete:
		if err := g.logs.Clear(); err != nil {
			writeJSON(w, 500, map[string]string{"error": "清空日志失败: " + err.Error()})
		} else {
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
	case requestPath == "/api/stream/logs" && r.Method == http.MethodGet:
		g.logs.ServeSSE(w, r, r.URL.Query().Get("level"))
	default:
		return false
	}
	return true
}

func (g *gateway) handleMihomoAPI(w http.ResponseWriter, r *http.Request, requestPath string) bool {
	client := &mihomo.Client{SettingsFile: g.config.settingsFile}
	switch {
	case requestPath == "/api/status" && r.Method == http.MethodGet:
		g.status(w, r, client)
	case requestPath == "/api/connection-stats" && r.Method == http.MethodGet:
		g.writeConnectionStats(w, r, client)
	case requestPath == "/api/traffic-history" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"samples": g.trafficHistory.Snapshot()})
	case requestPath == "/api/exit-location" && r.Method == http.MethodGet:
		g.writeExitLocation(w, r, client)
	case requestPath == "/api/settings/test" && r.Method == http.MethodPost:
		g.testController(w, r, client)
	case requestPath == "/api/proxies" && r.Method == http.MethodGet:
		g.orderedProxies(w, r, client)
	case requestPath == "/api/providers" && r.Method == http.MethodGet:
		g.forwardMihomo(w, r, client, http.MethodGet, "/providers/proxies", nil, 12*time.Second)
	case requestPath == "/api/rule-providers" && r.Method == http.MethodGet:
		g.forwardMihomo(w, r, client, http.MethodGet, "/providers/rules", nil, 12*time.Second)
	case requestPath == "/api/rules" && r.Method == http.MethodGet:
		g.writeRules(w, r, client)
	case requestPath == "/api/connections" && r.Method == http.MethodGet:
		g.writeConnections(w, r, client)
	case requestPath == "/api/connections" && r.Method == http.MethodDelete:
		g.mihomoMutation(w, r, client, http.MethodDelete, "/connections", nil, 12*time.Second)
	case strings.HasPrefix(requestPath, "/api/connections/") && r.Method == http.MethodDelete:
		id, ok := escapedTail(requestPath, "/api/connections/")
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "连接标识无效"})
			return true
		}
		g.mihomoMutation(w, r, client, http.MethodDelete, "/connections/"+id, nil, 12*time.Second)
	case requestPath == "/api/runtime-config" && r.Method == http.MethodGet:
		g.forwardMihomo(w, r, client, http.MethodGet, "/configs", nil, 12*time.Second)
	case requestPath == "/api/runtime-config" && r.Method == http.MethodPatch:
		body, ok := readLimitedBody(w, r, 12<<20)
		if !ok {
			return true
		}
		g.mihomoMutation(w, r, client, http.MethodPatch, "/configs", bytes.NewReader(body), 12*time.Second)
	case strings.HasPrefix(requestPath, "/api/proxies/") && r.Method == http.MethodPut:
		g.selectProxy(w, r, client, requestPath)
	case strings.HasPrefix(requestPath, "/api/rule-providers/") && r.Method == http.MethodPut:
		return g.handleRuleProviderOperation(w, r, client, requestPath)
	case strings.HasPrefix(requestPath, "/api/providers/"):
		return g.handleProviderOperation(w, r, client, requestPath)
	case strings.HasPrefix(requestPath, "/api/delay/") && r.Method == http.MethodGet:
		name, ok := escapedTail(requestPath, "/api/delay/")
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "节点名称无效"})
			return true
		}
		settings, err := client.LoadSettings()
		if err != nil {
			writeMihomoError(w, err)
			return true
		}
		query := url.Values{}
		query.Set("url", settings.HealthcheckURL)
		query.Set("timeout", strconv.Itoa(settings.HealthcheckTimeout))
		g.forwardMihomo(w, r, client, http.MethodGet, "/proxies/"+name+"/delay?"+query.Encode(), nil, time.Duration(settings.HealthcheckTimeout+3000)*time.Millisecond)
	case requestPath == "/api/stream/traffic" && r.Method == http.MethodGet:
		g.streamMihomoSSE(w, r, client, "/traffic")
	case requestPath == "/api/stream/memory" && r.Method == http.MethodGet:
		g.streamMihomoSSE(w, r, client, "/memory")
	default:
		return false
	}
	return true
}

func mihomoJSON(ctx context.Context, client *mihomo.Client, apiPath string) (map[string]any, error) {
	response, err := client.Do(ctx, http.MethodGet, apiPath, nil, 12*time.Second)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload := map[string]any{}
	if err := json.NewDecoder(io.LimitReader(response.Body, 12<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (g *gateway) testController(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	versionPayload, err := mihomoJSON(r.Context(), client, "/version")
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": versionPayload})
}

func (g *gateway) status(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	versionPayload, err := mihomoJSON(r.Context(), client, "/version")
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	configs, err := mihomoJSON(r.Context(), client, "/configs")
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	connections, err := mihomoJSON(r.Context(), client, "/connections")
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	memory := connections["memory"]
	if !positiveNumber(memory) {
		memory = nil
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"online": true, "version": versionPayload, "configs": configs,
		"connections": g.connectionStats(connections, memory),
	})
}

func positiveNumber(value any) bool {
	switch number := value.(type) {
	case float64:
		return number > 0
	case float32:
		return number > 0
	case int:
		return number > 0
	case int64:
		return number > 0
	case json.Number:
		parsed, err := number.Float64()
		return err == nil && parsed > 0
	default:
		return false
	}
}

func mihomoMemory(ctx context.Context, client *mihomo.Client, connectionMemory any) (any, error) {
	if positiveNumber(connectionMemory) {
		return connectionMemory, nil
	}
	response, err := client.Do(ctx, http.MethodGet, "/memory", nil, 4*time.Second)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	for attempt := 0; attempt < 2; attempt++ {
		payload := map[string]any{}
		if err := decoder.Decode(&payload); err != nil {
			return nil, err
		}
		if value := payload["inuse"]; positiveNumber(value) {
			return value, nil
		}
	}
	return nil, errors.New("Mihomo 内存接口未返回有效样本")
}

func (g *gateway) connectionStats(connections map[string]any, memory any) map[string]any {
	items, _ := connections["connections"].([]any)
	totals := g.trafficTotals.Observe(connections["uploadTotal"], connections["downloadTotal"])
	return map[string]any{"count": len(items), "uploadTotal": totals.Upload, "downloadTotal": totals.Download, "memory": memory}
}

func (g *gateway) writeConnectionStats(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	connections, err := mihomoJSON(r.Context(), client, "/connections")
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	memory, _ := mihomoMemory(r.Context(), client, connections["memory"])
	writeJSON(w, http.StatusOK, g.connectionStats(connections, memory))
}

func (g *gateway) writeConnections(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	payload, err := mihomoJSON(r.Context(), client, "/connections")
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	_, hasUpload := payload["uploadTotal"]
	_, hasDownload := payload["downloadTotal"]
	if hasUpload || hasDownload {
		totals := g.trafficTotals.Observe(payload["uploadTotal"], payload["downloadTotal"])
		payload["uploadTotal"], payload["downloadTotal"] = totals.Upload, totals.Download
	}
	writeJSON(w, http.StatusOK, payload)
}

func (g *gateway) orderedProxies(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	response, err := client.Do(r.Context(), http.MethodGet, "/proxies", nil, 12*time.Second)
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 12<<20)).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "解析 Mihomo 代理组失败: " + err.Error()})
		return
	}
	order, source, configPath := g.proxyGroupOrder(r.Context())
	payload["groupOrder"] = order
	payload["groupOrderSource"] = source
	payload["groupOrderPath"] = configPath
	writeJSON(w, http.StatusOK, payload)
}

func (g *gateway) proxyGroupOrder(ctx context.Context) ([]string, string, any) {
	var startup struct {
		Order      []string `json:"order"`
		ConfigPath string   `json:"configPath"`
	}
	if g.config.privilegedSocket != "" {
		if err := (privileged.Client{SocketPath: g.config.privilegedSocket}).GetJSON(ctx, "/config/proxy-group-order", &startup); err == nil && len(startup.Order) > 0 {
			return startup.Order, "startup", startup.ConfigPath
		}
	}
	if raw, err := os.ReadFile(g.config.managedConfigFile); err == nil {
		if order := configyaml.ProxyGroupOrder(string(raw)); len(order) > 0 {
			return order, "managed", g.config.managedConfigFile
		}
	}
	return []string{}, "api", nil
}

func (g *gateway) selectProxy(w http.ResponseWriter, r *http.Request, client *mihomo.Client, requestPath string) {
	group, ok := escapedTail(requestPath, "/api/proxies/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "策略组名称无效"})
		return
	}
	body, ok := readLimitedBody(w, r, 1<<20)
	if !ok {
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(body, &payload) != nil || strings.TrimSpace(payload.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少节点名称"})
		return
	}
	requestBody, _ := json.Marshal(map[string]string{"name": payload.Name})
	response, err := client.Do(r.Context(), http.MethodPut, "/proxies/"+group, bytes.NewReader(requestBody), 12*time.Second)
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	response.Body.Close()
	settings, err := client.LoadSettings()
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	if settings.PersistSelections {
		decodedGroup, _ := url.PathUnescape(group)
		if err := g.saveSelection(decodedGroup, payload.Name); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存策略组选择失败: " + err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (g *gateway) saveSelection(group, name string) error {
	g.selectionMu.Lock()
	defer g.selectionMu.Unlock()
	state := map[string]string{}
	if body, err := os.ReadFile(g.config.selectedFile); err == nil {
		_ = json.Unmarshal(body, &state)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	state[group] = name
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(g.config.selectedFile), 0o700); err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.%d.tmp", g.config.selectedFile, os.Getpid())
	if err := os.WriteFile(temporary, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, g.config.selectedFile); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (g *gateway) handleRuleProviderOperation(w http.ResponseWriter, r *http.Request, client *mihomo.Client, requestPath string) bool {
	tail := strings.TrimPrefix(requestPath, "/api/rule-providers/")
	name, operation, ok := strings.Cut(tail, "/")
	if !ok || operation != "update" {
		return false
	}
	escapedName, ok := escapedSegment(name)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Rule Provider 名称无效"})
		return true
	}
	result, err := g.updateRuleProvider(r.Context(), client, escapedName)
	if err != nil {
		writeMihomoError(w, err)
		return true
	}
	g.rulesChanged()
	writeJSON(w, http.StatusOK, result)
	return true
}

func (g *gateway) updateRuleProvider(ctx context.Context, client *mihomo.Client, name string) (map[string]any, error) {
	g.ruleProviderMu.Lock()
	defer g.ruleProviderMu.Unlock()
	providerPath := "/providers/rules/" + name
	primaryErr := mihomoRequest(ctx, client, http.MethodPut, providerPath, nil, 30*time.Second)
	if primaryErr == nil {
		return map[string]any{"ok": true, "method": "normal"}, nil
	}
	log.Printf("[Rule Provider] %s 常规更新失败: %v; 准备直连兜底", name, primaryErr)
	response, err := client.Do(ctx, http.MethodGet, "/configs", nil, 8*time.Second)
	if err != nil {
		return nil, ruleProviderError(name, primaryErr, err)
	}
	var runtime struct {
		Mode string `json:"mode"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&runtime)
	response.Body.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("Rule Provider %s 更新失败: 无法读取运行模式: %w", name, decodeErr)
	}
	previousMode := strings.ToLower(strings.TrimSpace(runtime.Mode))
	if previousMode == "" {
		previousMode = "rule"
	}
	switched := previousMode != "direct"
	if switched {
		if err := patchRuntimeMode(ctx, client, "direct"); err != nil {
			return nil, fmt.Errorf("Rule Provider %s 更新失败: 切换直连模式失败: %w", name, err)
		}
	}
	directErr := mihomoRequest(ctx, client, http.MethodPut, providerPath, nil, 30*time.Second)
	if switched {
		var restoreErr error
		for attempt := 0; attempt < 2; attempt++ {
			restoreErr = patchRuntimeMode(ctx, client, previousMode)
			if restoreErr == nil {
				break
			}
			if attempt == 0 {
				time.Sleep(300 * time.Millisecond)
			}
		}
		if restoreErr != nil {
			return nil, &mihomo.APIError{Status: http.StatusInternalServerError, Message: fmt.Sprintf("Rule Provider %s 恢复原运行模式失败: %v", name, restoreErr)}
		}
	}
	if directErr != nil {
		return nil, ruleProviderError(name, primaryErr, directErr)
	}
	return map[string]any{"ok": true, "method": "direct-fallback", "initialError": primaryErr.Error()}, nil
}

func ruleProviderError(name string, primaryErr, fallbackErr error) error {
	status := http.StatusBadGateway
	var apiError *mihomo.APIError
	if errors.As(fallbackErr, &apiError) {
		status = apiError.Status
	} else if errors.As(primaryErr, &apiError) {
		status = apiError.Status
	}
	return &mihomo.APIError{
		Status:  status,
		Message: fmt.Sprintf("Rule Provider %s 更新失败: 常规尝试: %v; 直连兜底: %v", name, primaryErr, fallbackErr),
	}
}

func patchRuntimeMode(ctx context.Context, client *mihomo.Client, mode string) error {
	body, _ := json.Marshal(map[string]string{"mode": mode})
	return mihomoRequest(ctx, client, http.MethodPatch, "/configs", bytes.NewReader(body), 8*time.Second)
}

func mihomoRequest(ctx context.Context, client *mihomo.Client, method, apiPath string, body io.Reader, timeout time.Duration) error {
	response, err := client.Do(ctx, method, apiPath, body, timeout)
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

func (g *gateway) handleProviderOperation(w http.ResponseWriter, r *http.Request, client *mihomo.Client, requestPath string) bool {
	tail := strings.TrimPrefix(requestPath, "/api/providers/")
	name, operation, ok := strings.Cut(tail, "/")
	if !ok || name == "" {
		return false
	}
	escapedName, ok := escapedSegment(name)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Provider 名称无效"})
		return true
	}
	switch {
	case operation == "update" && r.Method == http.MethodPut:
		g.mihomoMutation(w, r, client, http.MethodPut, "/providers/proxies/"+escapedName, nil, 30*time.Second)
	case operation == "healthcheck" && r.Method == http.MethodGet:
		g.mihomoMutation(w, r, client, http.MethodGet, "/providers/proxies/"+escapedName+"/healthcheck", nil, 30*time.Second)
	default:
		return false
	}
	return true
}

func escapedTail(requestPath, prefix string) (string, bool) {
	return escapedSegment(strings.TrimPrefix(requestPath, prefix))
}

func escapedSegment(value string) (string, bool) {
	decoded, err := url.PathUnescape(value)
	if err != nil || decoded == "" || strings.ContainsRune(decoded, '\x00') {
		return "", false
	}
	return url.PathEscape(decoded), true
}

func readLimitedBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return nil, false
	}
	if int64(len(body)) > limit {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "请求体过大"})
		return nil, false
	}
	if len(bytes.TrimSpace(body)) == 0 || !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON 格式错误"})
		return nil, false
	}
	return body, true
}

func (g *gateway) forwardMihomo(w http.ResponseWriter, r *http.Request, client *mihomo.Client, method, apiPath string, body io.Reader, timeout time.Duration) {
	response, err := client.Do(r.Context(), method, apiPath, body, timeout)
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func (g *gateway) mihomoMutation(w http.ResponseWriter, r *http.Request, client *mihomo.Client, method, apiPath string, body io.Reader, timeout time.Duration) {
	response, err := client.Do(r.Context(), method, apiPath, body, timeout)
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	response.Body.Close()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeMihomoError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	var apiError *mihomo.APIError
	if errors.As(err, &apiError) {
		status = apiError.Status
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (g *gateway) streamMihomoSSE(w http.ResponseWriter, r *http.Request, client *mihomo.Client, apiPath string) {
	response, err := client.Do(r.Context(), http.MethodGet, apiPath, nil, 0)
	if err != nil {
		writeMihomoError(w, err)
		return
	}
	defer response.Body.Close()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "当前服务不支持流式响应"})
		return
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", line)
		flusher.Flush()
	}
}

func (g *gateway) serveStatic(w http.ResponseWriter, r *http.Request, requestPath string) bool {
	rel := strings.TrimPrefix(requestPath, "/")
	if rel == "" {
		rel = "index.html"
	}
	decoded, err := url.PathUnescape(rel)
	if err != nil || !validStaticPath(decoded) {
		return false
	}
	fileName := filepath.Join(g.config.publicDir, filepath.FromSlash(decoded))
	file, err := os.Open(fileName)
	if err != nil {
		return false
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		return false
	}
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	if contentType := mime.TypeByExtension(filepath.Ext(fileName)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), file)
	return true
}

func validStaticPath(value string) bool {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") {
		return false
	}
	clean := path.Clean("/" + value)
	return clean == "/"+value && value != ".." && !strings.HasPrefix(value, "../")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func removeStaleSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("拒绝覆盖非 Socket 文件: %s", socketPath)
	}
	return os.Remove(socketPath)
}

func run() error {
	cfg := loadConfig()
	if err := removeStaleSocket(cfg.socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", cfg.socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(cfg.socketPath)
	if err := os.Chmod(cfg.socketPath, 0o660); err != nil {
		return err
	}

	gateway := newGateway(cfg)
	mihomoClient := &mihomo.Client{SettingsFile: cfg.settingsFile}
	collectorContext, stopCollector := context.WithCancel(context.Background())
	defer stopCollector()
	go gateway.logs.Run(collectorContext, cfg.settingsFile)
	go gateway.trafficTotals.Run(collectorContext, mihomoClient)
	go gateway.trafficHistory.Run(collectorContext, mihomoClient)
	go gateway.runStartupTasks(collectorContext)
	go gateway.runProfileScheduler(collectorContext)
	gateway.rulesSnapshot.scheduleRefresh(cfg.settingsFile, time.Second)
	server := &http.Server{
		Handler:           gateway,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		// fnOS stops the unprivileged web service before the root helper, so the
		// managed Core is still available for one final durable counter sample.
		sampleContext, cancelSample := context.WithTimeout(context.Background(), 2*time.Second)
		gateway.trafficTotals.sample(sampleContext, mihomoClient)
		if err := gateway.trafficHistory.Save(); err != nil {
			log.Printf("保存实时流量历史失败: %v", err)
		}
		cancelSample()
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelShutdown()
		_ = server.Shutdown(shutdownContext)
	}()

	log.Printf("Clash for fnOS %s Go gateway started on %s", version, cfg.socketPath)
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil && !errors.Is(err, io.EOF) {
		log.Fatal(err)
	}
}
