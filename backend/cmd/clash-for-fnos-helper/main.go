package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const maxBody = 12 << 20

var version = "dev"

type helperConfig struct {
	socket            string
	etcDir            string
	varDir            string
	appDir            string
	managedCoreDir    string
	managedCore       string
	managedPID        string
	managedLog        string
	managedConfigDir  string
	managedConfig     string
	coreModeFile      string
	bootstrapMetaFile string
	stageDir          string
	backupDir         string
	proxySettingsFile string
	iconSettingsFile  string
}

type transaction struct {
	Target             string
	Backup             string
	Candidate          string
	CreatedAt          time.Time
	Activation         string
	Restart            bool
	Validated          bool
	ValidationRequired bool
	Mode               os.FileMode
	UID                int
	GID                int
}

type helper struct {
	config       helperConfig
	mu           sync.Mutex
	transactions map[string]*transaction
	coreTx       map[string]*transaction
	managed      *os.Process
	bootstrap    map[string]any
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func loadConfig() helperConfig {
	etcDir := env("TRIM_PKGETC", "/tmp/clash-for-fnos-etc")
	varDir := env("TRIM_PKGVAR", "/tmp/clash-for-fnos-var")
	appDir := env("TRIM_APPDEST", filepath.Clean(filepath.Join(filepath.Dir(os.Args[0]), "..", "..")))
	managedCoreDir := filepath.Join(varDir, "managed-core")
	managedConfigDir := filepath.Join(etcDir, "mihomo")
	return helperConfig{
		socket: env("PRIV_SOCKET_PATH", "/tmp/clash-for-fnos-priv.sock"), etcDir: etcDir, varDir: varDir, appDir: appDir,
		managedCoreDir: managedCoreDir, managedCore: filepath.Join(managedCoreDir, "mihomo"), managedPID: filepath.Join(managedCoreDir, "mihomo.pid"), managedLog: filepath.Join(managedCoreDir, "mihomo.log"),
		managedConfigDir: managedConfigDir, managedConfig: filepath.Join(managedConfigDir, "config.yaml"), coreModeFile: filepath.Join(etcDir, "core-mode.json"), bootstrapMetaFile: filepath.Join(etcDir, "managed-core-meta.json"),
		stageDir: filepath.Join(varDir, "core-stage"), backupDir: filepath.Join(etcDir, "system-backups"), proxySettingsFile: filepath.Join(etcDir, "proxy-environment.json"), iconSettingsFile: filepath.Join(etcDir, "app-icon.json"),
	}
}

func newHelper(cfg helperConfig) *helper {
	return &helper{config: cfg, transactions: map[string]*transaction{}, coreTx: map[string]*transaction{}, bootstrap: map[string]any{"state": "idle", "progress": 0, "delivery": "bundled"}}
}

func (h *helper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if r.Method == http.MethodGet {
		var result any
		var err error
		switch path {
		case "/status":
			result, err = h.systemStatus(r.Context())
		case "/bootstrap/status":
			result = h.bootstrapSnapshot()
		case "/config/proxy-group-order":
			result, err = h.proxyGroupOrder()
		case "/config/active-raw":
			result, err = h.activeRaw()
		case "/network/status":
			result, err = h.networkStatus(r.Context())
		case "/geo/status":
			result, err = h.geoStatus()
		case "/app/icon/status":
			result, err = h.iconStatus()
		case "/system/proxy-environment":
			result, err = h.proxyEnvironment()
		default:
			writeJSON(w, 404, map[string]string{"error": "Not found"})
			return
		}
		h.writeResult(w, result, err)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, 404, map[string]string{"error": "Not found"})
		return
	}
	var body map[string]any
	if !decodeBody(w, r, &body) {
		return
	}
	var result any
	var err error
	switch path {
	case "/bootstrap/retry":
		result, err = h.ensureBootstrap(r.Context(), true, "")
	case "/config/sync":
		result, err = h.prepareConfigCandidate(r.Context(), stringField(body, "content"), !boolField(body, "skipValidation"), false)
	case "/config/activate":
		result, err = h.activateConfig(r.Context(), stringField(body, "txId"))
	case "/config/validate":
		result, err = h.validateConfigTransaction(r.Context(), stringField(body, "txId"))
	case "/config/rollback":
		result, err = h.rollbackConfig(r.Context(), stringField(body, "txId"))
	case "/config/commit":
		result, err = h.commitConfig(stringField(body, "txId"))
	case "/config/inspect-path":
		result, err = h.inspectPath(stringField(body, "path"))
	case "/config/read-path":
		result, err = h.readPath(stringField(body, "path"))
	case "/network/update":
		result, err = h.updateNetwork(r.Context(), body)
	case "/geo/settings":
		result, err = h.prepareGeoSettings(r.Context(), body)
	case "/geo/download":
		result, err = h.downloadMissingGeoAsset(r.Context(), stringField(body, "key"))
	case "/network/tun":
		enabled, ok := body["enabled"].(bool)
		if !ok {
			err = fail(400, "enabled 必须是布尔值")
		} else {
			result, err = h.prepareTunToggle(r.Context(), enabled)
		}
	case "/app/icon/update":
		result, err = h.updateIcon(stringField(body, "iconId"))
	case "/system/proxy-environment/update":
		result, err = h.updateProxyEnvironment(body)
	case "/system/proxy-environment/sync":
		result, err = h.syncProxyEnvironment()
	case "/core/select-mode":
		result, err = h.selectMode(r.Context(), stringField(body, "mode"))
	case "/core/restart-managed":
		result, err = h.restartManaged(r.Context())
	case "/core/install":
		result, err = h.installCore(r.Context(), stringField(body, "stagePath"), stringField(body, "expectedVersion"), boolField(body, "restart"))
	case "/core/rollback":
		result, err = h.rollbackCore(r.Context(), stringField(body, "txId"), boolField(body, "restart"))
	case "/core/commit":
		result, err = h.commitCore(stringField(body, "txId"))
	default:
		writeJSON(w, 404, map[string]string{"error": "Not found"})
		return
	}
	h.writeResult(w, result, err)
}

func (h *helper) writeResult(w http.ResponseWriter, result any, err error) {
	if err != nil {
		status := 500
		var api *apiError
		if errors.As(err, &api) {
			status = api.status
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, result)
}

func (h *helper) bootstrapSnapshot() map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	copy := map[string]any{}
	for key, value := range h.bootstrap {
		copy[key] = value
	}
	return copy
}

type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string           { return e.message }
func fail(status int, message string) error { return &apiError{status: status, message: message} }
func stringField(body map[string]any, key string) string {
	value, _ := body[key].(string)
	return strings.TrimSpace(value)
}
func boolField(body map[string]any, key string) bool { value, _ := body[key].(bool); return value }
func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		writeJSON(w, 413, map[string]string{"error": "请求体过大"})
		return false
	}
	if len(body) == 0 || json.Unmarshal(body, target) != nil {
		writeJSON(w, 400, map[string]string{"error": "JSON 格式错误"})
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func removeSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("拒绝覆盖非 Socket 文件: %s", path)
	}
	return os.Remove(path)
}

func run() error {
	if os.Geteuid() != 0 && os.Getenv("CLASH_HELPER_ALLOW_NON_ROOT") != "1" {
		return errors.New("clash-for-fnos-helper 必须以 root 运行")
	}
	cfg := loadConfig()
	for _, dir := range []string{cfg.etcDir, cfg.varDir, cfg.managedCoreDir, cfg.managedConfigDir, cfg.stageDir, cfg.backupDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	if err := removeSocket(cfg.socket); err != nil {
		return err
	}
	listener, err := net.Listen("unix", cfg.socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(cfg.socket)
	if err = os.Chmod(cfg.socket, 0o660); err != nil {
		return err
	}
	h := newHelper(cfg)
	if _, err := os.Stat(cfg.proxySettingsFile); err == nil {
		if _, syncErr := h.syncProxyEnvironment(); syncErr != nil {
			log.Printf("Proxy environment startup sync failed: %v", syncErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("Proxy environment settings check failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, err := h.ensureBootstrap(ctx, false, "")
		if err != nil {
			log.Printf("Mihomo bootstrap failed: %v", err)
		}
	}()
	server := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
		h.stopManaged()
		shutdown, cancelShutdown := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancelShutdown()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Clash for fnOS %s Go helper started on %s", version, cfg.socket)
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
