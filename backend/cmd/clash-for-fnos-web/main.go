package main

import (
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
	"strings"
	"syscall"
	"time"
)

const appName = "clash-for-fnos"

var version = "dev"

type config struct {
	socketPath   string
	upstreamPath string
	publicDir    string
	gateway      string
}

type gateway struct {
	config config
	proxy  *httputil.ReverseProxy
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func loadConfig() config {
	return config{
		socketPath:   env("SOCKET_PATH", "/tmp/clash-for-fnos.sock"),
		upstreamPath: env("UPSTREAM_SOCKET_PATH", "/tmp/clash-for-fnos-node.sock"),
		publicDir:    env("PUBLIC_DIR", "./public"),
		gateway:      strings.TrimSuffix(env("GATEWAY_PREFIX", "/app/"+appName), "/"),
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
