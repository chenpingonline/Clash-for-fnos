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
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/configyaml"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/privileged"
)

const appName = "clash-for-fnos"

var version = "dev"

type config struct {
	socketPath        string
	upstreamPath      string
	publicDir         string
	gateway           string
	settingsFile      string
	selectedFile      string
	managedConfigFile string
	privilegedSocket  string
}

type gateway struct {
	config         config
	proxy          *httputil.ReverseProxy
	selectionMu    sync.Mutex
	ruleProviderMu sync.Mutex
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func loadConfig() config {
	return config{
		socketPath:        env("SOCKET_PATH", "/tmp/clash-for-fnos.sock"),
		upstreamPath:      env("UPSTREAM_SOCKET_PATH", "/tmp/clash-for-fnos-node.sock"),
		publicDir:         env("PUBLIC_DIR", "./public"),
		gateway:           strings.TrimSuffix(env("GATEWAY_PREFIX", "/app/"+appName), "/"),
		settingsFile:      filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "settings.json"),
		selectedFile:      filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "selected.json"),
		managedConfigFile: filepath.Join(env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc"), "config.yaml"),
		privilegedSocket:  env("PRIV_SOCKET_PATH", "/tmp/clash-for-fnos-priv.sock"),
	}
}

func newGateway(cfg config) *gateway {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", cfg.upstreamPath)
		},
		ResponseHeaderTimeout: 3 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
	}
	proxy := &httputil.ReverseProxy{
		Transport:     transport,
		FlushInterval: -1,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(&url.URL{Scheme: "http", Host: "legacy.internal"})
			request.Out.Host = "localhost"
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Node 兼容服务不可用: " + err.Error()})
		},
	}
	return &gateway{config: cfg, proxy: proxy}
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
			"backend": "go", "compatibilityBackend": "node",
		})
		return
	}
	if g.handleMihomoAPI(w, r, requestPath) {
		return
	}
	if strings.HasPrefix(requestPath, "/api/") {
		g.proxy.ServeHTTP(w, r)
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

func (g *gateway) handleMihomoAPI(w http.ResponseWriter, r *http.Request, requestPath string) bool {
	client := &mihomo.Client{SettingsFile: g.config.settingsFile}
	switch {
	case requestPath == "/api/proxies" && r.Method == http.MethodGet:
		g.orderedProxies(w, r, client)
	case requestPath == "/api/providers" && r.Method == http.MethodGet:
		g.forwardMihomo(w, r, client, http.MethodGet, "/providers/proxies", nil, 12*time.Second)
	case requestPath == "/api/rule-providers" && r.Method == http.MethodGet:
		g.forwardMihomo(w, r, client, http.MethodGet, "/providers/rules", nil, 12*time.Second)
	case requestPath == "/api/rules" && r.Method == http.MethodGet:
		g.forwardMihomo(w, r, client, http.MethodGet, "/rules", nil, 12*time.Second)
	case requestPath == "/api/connections" && r.Method == http.MethodGet:
		g.forwardMihomo(w, r, client, http.MethodGet, "/connections", nil, 12*time.Second)
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
		g.streamTraffic(w, r, client)
	default:
		return false
	}
	return true
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

func (g *gateway) streamTraffic(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	response, err := client.Do(r.Context(), http.MethodGet, "/traffic", nil, 0)
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

	server := &http.Server{
		Handler:           newGateway(cfg),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
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
