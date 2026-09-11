package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/configyaml"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var safeSystemConfigs = map[string]bool{"/etc/mihomo/config.yaml": true, "/etc/clash/config.yaml": true, "/usr/local/etc/mihomo/config.yaml": true, "/usr/local/etc/clash/config.yaml": true, "/opt/mihomo/config.yaml": true, "/var/lib/mihomo/config.yaml": true, "/root/.config/mihomo/config.yaml": true, "/root/.config/clash/config.yaml": true}

func randomID() string {
	body := make([]byte, 12)
	_, _ = rand.Read(body)
	return hex.EncodeToString(body)
}
func atomicWrite(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(temp, body, mode); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
func copyFile(src, dst string, mode os.FileMode) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return atomicWrite(dst, body, mode)
}
func safeStamp() string { return time.Now().UTC().Format("2006-01-02T15-04-05.000Z") }

type processInfo struct {
	PID                        int
	Exe, ConfigPath, ConfigDir string
	Managed                    bool
}

func readCmdline(pid int) []string {
	body, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil
	}
	parts := strings.Split(string(body), "\x00")
	out := []string{}
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
func option(args []string, names ...string) string {
	for index, arg := range args {
		for _, name := range names {
			if arg == name && index+1 < len(args) {
				return args[index+1]
			}
			if strings.HasPrefix(arg, name+"=") {
				return strings.TrimPrefix(arg, name+"=")
			}
		}
	}
	return ""
}
func optionalConfigDir(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

func cleanOptionalPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Clean(path)
}

func (h *helper) processes() []processInfo {
	entries, _ := os.ReadDir("/proc")
	items := []processInfo{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		args := readCmdline(pid)
		if len(args) == 0 {
			continue
		}
		exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		base := strings.ToLower(filepath.Base(exe))
		if !strings.Contains(base, "mihomo") && !strings.Contains(base, "clash-meta") {
			continue
		}
		dir := option(args, "-d", "--dir", "--home-dir")
		file := option(args, "-f", "--config", "--config-file", "-config")
		if file == "" && dir != "" {
			file = filepath.Join(dir, "config.yaml")
		}
		if file != "" && !filepath.IsAbs(file) {
			cwd, _ := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
			file = filepath.Join(cwd, file)
		}
		items = append(items, processInfo{PID: pid, Exe: exe, ConfigPath: cleanOptionalPath(file), ConfigDir: cleanOptionalPath(dir), Managed: filepath.Clean(exe) == h.config.managedCore})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].PID < items[j].PID })
	return items
}
func (h *helper) managedProcess() *processInfo {
	body, err := os.ReadFile(h.config.managedPID)
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil || pid <= 0 {
		return nil
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return nil
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil || filepath.Clean(exe) != filepath.Clean(h.config.managedCore) {
		return nil
	}
	return &processInfo{PID: pid, Exe: exe, ConfigPath: h.config.managedConfig, ConfigDir: h.config.managedConfigDir, Managed: true}
}
func (h *helper) primary() *processInfo {
	mode := h.readMode()
	if mode != "external" {
		if managed := h.managedProcess(); managed != nil {
			return managed
		}
	}
	return selectProcess(h.processes(), mode)
}

func selectProcess(items []processInfo, mode string) *processInfo {
	for _, item := range items {
		if mode == "managed" && item.Managed {
			return &item
		}
		if mode == "external" && !item.Managed {
			return &item
		}
	}

	return nil
}
func (h *helper) externalInstallation() *processInfo {
	for _, proc := range h.processes() {
		if !proc.Managed {
			copy := proc
			return &copy
		}
	}
	for _, binary := range []string{"/usr/local/bin/mihomo", "/usr/bin/mihomo", "/opt/mihomo/mihomo", "/usr/local/bin/clash-meta", "/usr/bin/clash-meta"} {
		if stat, err := os.Stat(binary); err == nil && stat.Mode().IsRegular() {
			configs := []string{"/etc/mihomo/config.yaml", "/etc/clash/config.yaml", "/usr/local/etc/mihomo/config.yaml", "/opt/mihomo/config.yaml"}
			configPath := ""
			for _, candidate := range configs {
				if fileExists(candidate) {
					configPath = candidate
					break
				}
			}
			return &processInfo{Exe: binary, ConfigPath: configPath, ConfigDir: optionalConfigDir(configPath)}
		}
	}
	return nil
}
func (h *helper) readMode() string {
	var data struct {
		Mode string `json:"mode"`
	}
	body, _ := os.ReadFile(h.config.coreModeFile)
	_ = json.Unmarshal(body, &data)
	if data.Mode == "external" || data.Mode == "managed" {
		return data.Mode
	}
	return "managed"
}
func (h *helper) writeMode(mode string) error {
	body, _ := json.MarshalIndent(map[string]any{"mode": mode, "updatedAt": time.Now().UnixMilli()}, "", "  ")
	return atomicWrite(h.config.coreModeFile, body, 0o600)
}

func (h *helper) ensureManagedConfig() error {
	if _, err := os.Stat(h.config.managedConfig); err == nil {
		return nil
	}
	secret := randomID()
	content := fmt.Sprintf("mixed-port: 7890\nallow-lan: false\nmode: rule\nlog-level: info\nexternal-controller: 127.0.0.1:9090\nsecret: %q\n", secret)
	return atomicWrite(h.config.managedConfig, []byte(content), 0o640)
}

// Probe configured listeners instead of blocking all other Mihomo processes.
func (h *helper) checkManagedPorts() error {
	raw, err := os.ReadFile(h.config.managedConfig)
	if err != nil {
		return err
	}
	probe := func(network, address, label string) error {
		if address == "" {
			return nil
		}
		var closeListener func() error
		if network == "udp" {
			listener, e := net.ListenPacket(network, address)
			err = e
			if e == nil {
				closeListener = listener.Close
			}
		} else {
			listener, e := net.Listen(network, address)
			err = e
			if e == nil {
				closeListener = listener.Close
			}
		}
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) {
				return fmt.Errorf("托管 Core 端口占用：%s (%s %s): %w", label, network, address, err)
			}
			return fmt.Errorf("托管 Core 无法监听 %s (%s %s)，请检查监听地址或权限: %w", label, network, address, err)
		}
		return closeListener()
	}
	controller, _, _ := parseController(string(raw))
	if err := probe("tcp", controller, "Controller"); err != nil {
		return err
	}
	host := "127.0.0.1"
	if yamlBoolean(string(raw), "allow-lan", false) {
		host = "0.0.0.0"
		if bind, ok := yamlScalarValue(string(raw), "bind-address"); ok && bind != "" && bind != "*" {
			host = bind
		}
	}
	for _, key := range []string{"mixed-port", "port", "socks-port", "redir-port", "tproxy-port"} {
		value, _ := yamlScalarValue(string(raw), key)
		port, _ := strconv.Atoi(value)
		if port <= 0 {
			continue
		}
		address := net.JoinHostPort(host, strconv.Itoa(port))
		if err := probe("tcp", address, key); err != nil {
			return err
		}
		if key == "mixed-port" || key == "socks-port" || key == "tproxy-port" {
			if err := probe("udp", address, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *helper) startManaged() (*processInfo, error) {
	if proc := h.primary(); proc != nil && proc.Managed {
		return proc, nil
	}
	if err := h.ensureManagedConfig(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(h.config.managedCore); err != nil {
		return nil, err
	}
	if err := h.checkManagedPorts(); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(h.config.managedLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(h.config.managedCore, "-d", h.config.managedConfigDir, "-f", h.config.managedConfig)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	h.managed = cmd.Process
	_ = atomicWrite(h.config.managedPID, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600)
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = logFile.Close()
		// An older process must not remove the PID file of its replacement.
		if body, readErr := os.ReadFile(h.config.managedPID); readErr == nil && strings.TrimSpace(string(body)) == strconv.Itoa(cmd.Process.Pid) {
			_ = os.Remove(h.config.managedPID)
		}
		exited <- err
	}()
	raw, _ := os.ReadFile(h.config.managedConfig)
	controller, secret, _ := parseController(string(raw))
	host, port, splitErr := net.SplitHostPort(controller)
	if splitErr == nil && (host == "" || host == "0.0.0.0" || host == "::") {
		controller = net.JoinHostPort("127.0.0.1", port)
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: transport}
	deadline := time.Now().Add(8 * time.Second)
	for {
		select {
		case err := <-exited:
			return nil, fmt.Errorf("托管 Mihomo 启动后退出 (%v)，请检查端口占用、配置及日志 %s", err, h.config.managedLog)
		default:
		}
		request, err := http.NewRequest(http.MethodGet, "http://"+controller+"/version", nil)
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+secret)
			response, err := client.Do(request)
			if err == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					break
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("托管 Core 已启动，但 Controller 在 8 秒内未就绪，请检查监听地址、端口及日志 %s", h.config.managedLog)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return &processInfo{PID: cmd.Process.Pid, Exe: h.config.managedCore, ConfigPath: h.config.managedConfig, ConfigDir: h.config.managedConfigDir, Managed: true}, nil
}

func processRunning(pid int) bool {
	if pid <= 1 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terminateManagedPID(pid int, gracefulTimeout time.Duration) {
	if !processRunning(pid) {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(gracefulTimeout)
	for processRunning(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if processRunning(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func (h *helper) stopManaged() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopManagedLocked()
}

func (h *helper) stopManagedLocked() {
	// Never signal an unrelated process through a stale PID file.
	if proc := h.managedProcess(); proc != nil {
		terminateManagedPID(proc.PID, 3*time.Second)
	}
	h.managed = nil
	_ = os.Remove(h.config.managedPID)
}

func (h *helper) startCore(ctx context.Context) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.readMode() != "managed" {
		return nil, fail(409, "外部 Core 请通过原有服务启动")
	}
	return h.ensureBootstrapLocked(ctx, true, "managed")
}

func (h *helper) stopCore(ctx context.Context) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.readMode() != "managed" {
		return nil, fail(409, "外部 Core 请通过原有服务停止")
	}
	proc := h.primary()
	h.stopManagedLocked()
	if proc != nil {
		deadline := time.Now().Add(time.Second)
		for processRunning(proc.PID) {
			if time.Now().After(deadline) {
				return nil, fail(409, "托管 Core 尚未退出，请稍后刷新状态")
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	h.setBootstrap(map[string]any{"state": "stopped", "mode": "managed", "message": "托管 Core 已停止，可点击启动内核或重新检测恢复", "progress": 0})
	return map[string]any{"ok": true, "mode": "managed", "state": "stopped"}, nil
}

func (h *helper) restartManaged(ctx context.Context) (map[string]any, error) {
	proc := h.primary()
	if proc != nil && !proc.Managed {
		return nil, fail(409, "当前不是 Manager 托管的 Mihomo Core，不能自动重启")
	}
	if proc == nil && h.readMode() != "managed" {
		return nil, fail(409, "当前没有可重启的 Manager 托管 Mihomo Core")
	}
	if proc != nil {
		h.stopManaged()
		deadline := time.Now().Add(1500 * time.Millisecond)
		for syscall.Kill(proc.PID, 0) == nil {
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("旧 Mihomo Core 进程 %d 未及时退出", proc.PID)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(100 * time.Millisecond):
	}
	restarted, err := h.startManaged()
	if err != nil {
		return nil, fmt.Errorf("重启 Manager 托管 Mihomo Core 失败: %w", err)
	}
	return map[string]any{"ok": true, "mode": "managed", "pid": restarted.PID}, nil
}

func (h *helper) installBundled() error {
	metaPath := filepath.Join(h.config.appDir, "core", "bundled-core.json")
	var meta struct {
		Tag    string `json:"tag"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	}
	body, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(body, &meta); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Dir(metaPath))
	if err != nil {
		return err
	}
	asset := ""
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "mihomo-linux-") && strings.HasSuffix(entry.Name(), ".gz") {
			if asset != "" {
				return errors.New("内置 Core 资产不唯一")
			}
			asset = filepath.Join(filepath.Dir(metaPath), entry.Name())
		}
	}
	if asset == "" || !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(meta.Tag) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(strings.ToLower(meta.SHA256)) {
		return errors.New("内置 Core 清单无效")
	}
	compressed, err := os.ReadFile(asset)
	if err != nil {
		return err
	}
	if len(compressed) == 0 || len(compressed) > 80<<20 || (meta.Size > 0 && int64(len(compressed)) != meta.Size) {
		return errors.New("内置 Core 大小校验失败")
	}
	sum := sha256.Sum256(compressed)
	if hex.EncodeToString(sum[:]) != strings.ToLower(meta.SHA256) {
		return errors.New("内置 Core SHA-256 校验失败")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return err
	}
	defer reader.Close()
	binary, err := io.ReadAll(io.LimitReader(reader, 128<<20))
	if err != nil {
		return err
	}
	if len(binary) == 0 {
		return errors.New("内置 Core 为空")
	}
	candidate := h.config.managedCore + ".bundled.new"
	if err = atomicWrite(candidate, binary, 0o755); err != nil {
		return err
	}
	defer os.Remove(candidate)
	out, err := exec.Command(candidate, "-v").CombinedOutput()
	if err != nil || !strings.Contains(string(out), strings.TrimPrefix(meta.Tag, "v")) {
		return fmt.Errorf("内置 Core 版本校验失败: %s", strings.TrimSpace(string(out)))
	}
	return os.Rename(candidate, h.config.managedCore)
}

func (h *helper) ensureBootstrap(ctx context.Context, force bool, requested string) (result map[string]any, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ensureBootstrapLocked(ctx, force, requested)
}

func (h *helper) ensureBootstrapLocked(ctx context.Context, force bool, requested string) (result map[string]any, err error) {
	defer func() {
		if err != nil {
			if errors.Is(err, context.Canceled) && h.bootstrapSnapshot()["state"] == "canceling" {
				result = h.setBootstrap(map[string]any{"state": "download-required", "mode": "managed", "message": "Core 下载已停止，可重新下载", "progress": 0, "delivery": "online"})
				err = nil
				return
			}
			failure := map[string]any{"state": "error", "mode": h.readMode(), "message": "Mihomo Core 启用失败", "error": err.Error(), "progress": 0}
			if delivery := h.bootstrapSnapshot()["delivery"]; delivery != nil {
				failure["delivery"] = delivery
			}
			h.setBootstrap(failure)
		}
	}()
	h.setBootstrap(map[string]any{"state": "checking", "message": "正在检测 Mihomo Core", "progress": 10, "delivery": "bundled"})
	external := (*processInfo)(nil)
	managed := (*processInfo)(nil)
	for _, proc := range h.processes() {
		if proc.Managed {
			copy := proc
			managed = &copy
		} else if external == nil {
			copy := proc
			external = &copy
		}
	}
	if external == nil {
		external = h.externalInstallation()
	}
	mode := requested
	if mode == "" {
		mode = h.readMode()
	}
	if mode == "external" {
		if external == nil {
			return nil, fail(404, "未检测到可用的外部 Mihomo Core")
		}
		if err = h.writeMode("external"); err != nil {
			return nil, err
		}
		h.stopManagedLocked()
		state, message := "ready", "已检测到外部 Mihomo 进程，连接状态以 Controller 检测结果为准"
		if external.PID == 0 {
			state, message = "external-stopped", "已选择外部 Core，请先通过原有服务启动 Mihomo"
		}
		return h.setBootstrap(map[string]any{"state": state, "mode": "external", "message": message, "progress": 100, "pid": nullableInt(external.PID), "binaryPath": external.Exe, "configPath": nullable(external.ConfigPath), "delivery": "external"}), nil
	}
	if err = h.writeMode("managed"); err != nil {
		return nil, err
	}
	delivery := "bundled"
	if managed == nil {
		if _, err := os.Stat(h.config.managedCore); err != nil {
			onlineOnly := fileExists(filepath.Join(h.config.appDir, "core", "online-core.json"))
			if onlineOnly && !force {
				return h.setBootstrap(map[string]any{"state": "download-required", "mode": "managed", "message": "all 通用包未内置 Mihomo Core，请下载后启用", "progress": 0, "delivery": "online"}), nil
			}
			if onlineOnly {
				delivery = "online"
				h.setBootstrap(map[string]any{"state": "checking", "mode": "managed", "message": "正在获取官方 Mihomo Core 版本", "progress": 0, "delivery": delivery})
				if _, err = h.downloadLatestCore(ctx); err != nil {
					return nil, err
				}
			} else {
				h.setBootstrap(map[string]any{"state": "installing", "mode": "managed", "message": "正在安装内置 Mihomo Core", "progress": 45, "delivery": delivery})
				if err = h.installBundled(); err != nil {
					delivery = "online"
					h.setBootstrap(map[string]any{"state": "checking", "mode": "managed", "message": "正在获取官方 Mihomo Core 版本", "progress": 0, "delivery": delivery})
					if _, err = h.downloadLatestCore(ctx); err != nil {
						return nil, err
					}
				}
			}
		}
		h.setBootstrap(map[string]any{"state": "starting", "mode": "managed", "message": "正在启动 Mihomo Core", "progress": 0, "delivery": delivery})
		proc, err := h.startManaged()
		if err != nil {
			return nil, err
		}
		managed = proc
	}
	return h.setBootstrap(map[string]any{"state": "ready", "mode": "managed", "message": "Manager 托管 Mihomo Core 已运行", "progress": 100, "pid": managed.PID, "binaryPath": h.config.managedCore, "configPath": h.config.managedConfig, "delivery": delivery}), nil
}
func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
func (h *helper) selectMode(ctx context.Context, mode string) (map[string]any, error) {
	if mode != "external" && mode != "managed" {
		return nil, fail(400, "Core 模式只能选择 external 或 managed")
	}
	return h.ensureBootstrap(ctx, true, mode)
}

func readVersion(binary string) string {
	if binary == "" {
		return ""
	}
	out, _ := exec.Command(binary, "-v").CombinedOutput()
	match := regexp.MustCompile(`v?\d+\.\d+\.\d+`).FindString(string(out))
	return match
}
func (h *helper) systemStatus(ctx context.Context) (map[string]any, error) {
	proc := h.primary()
	mode := h.readMode()
	result := map[string]any{"privileged": true, "available": true, "mode": mode, "coreMode": h.readMode(), "bootstrap": h.bootstrapSnapshot(), "canRestartService": proc != nil && proc.Managed, "managedMixedPort": 7890}
	if proc != nil {
		result["pid"], result["binaryPath"], result["configPath"], result["binaryVersion"] = proc.PID, proc.Exe, proc.ConfigPath, readVersion(proc.Exe)
	}
	if proc != nil && !proc.Managed && proc.ConfigPath != "" && proc.ConfigPath != "." {
		if raw, err := os.ReadFile(proc.ConfigPath); err == nil {
			controller, secret, _ := parseController(string(raw))
			// Wildcard listeners are reached over loopback from the local web service.
			host, port, splitErr := net.SplitHostPort(controller)
			if splitErr == nil && (host == "0.0.0.0" || host == "::" || host == "") {
				controller = net.JoinHostPort("127.0.0.1", port)
			}
			result["detectedController"], result["detectedSecret"], result["detectedSecretPresent"] = "http://"+controller, secret, true
		}
	}
	if raw, err := os.ReadFile(h.config.managedConfig); err == nil {
		controller, secret, mixed := parseController(string(raw))
		result["managedController"], result["managedSecret"], result["managedMixedPort"] = "http://"+controller, secret, mixed
	}
	return result, nil
}

func parseController(raw string) (string, string, int) {
	controller := "127.0.0.1:9090"
	secret := ""
	mixed := 7890
	for _, line := range strings.Split(raw, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "external-controller:") {
			controller = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trim, "external-controller:")), "\"'")
			if strings.HasPrefix(controller, ":") {
				controller = "127.0.0.1" + controller
			}
		}
		if strings.HasPrefix(trim, "secret:") {
			secret = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trim, "secret:")), "\"'")
		}
		if strings.HasPrefix(trim, "mixed-port:") {
			mixed, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trim, "mixed-port:")))
		}
	}
	return controller, secret, mixed
}

func (h *helper) allowedPath(input string) (string, error) {
	path := filepath.Clean(strings.TrimSpace(input))
	if !filepath.IsAbs(path) || strings.HasPrefix(path, "/proc/") || strings.HasPrefix(path, "/sys/") || strings.HasPrefix(path, "/dev/") {
		return "", fail(400, "配置路径不安全")
	}
	proc := h.primary()
	if path == h.config.managedConfig || safeSystemConfigs[path] || (proc != nil && path == proc.ConfigPath) {
		return path, nil
	}
	return "", fail(403, "不允许读取该系统路径")
}
func (h *helper) inspectPath(input string) (map[string]any, error) {
	path, err := h.allowedPath(input)
	if err != nil {
		return nil, err
	}
	stat, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{"exists": false, "readable": false, "permissionDenied": false}, nil
	}
	if err != nil {
		return map[string]any{"exists": true, "readable": false, "permissionDenied": errors.Is(err, os.ErrPermission)}, nil
	}
	real, _ := filepath.EvalSymlinks(path)
	return map[string]any{"exists": stat.Mode().IsRegular(), "readable": stat.Mode().IsRegular(), "permissionDenied": false, "size": stat.Size(), "mtime": stat.ModTime().UnixMilli(), "realPath": real, "dev": 0, "ino": 0}, nil
}
func (h *helper) readPath(input string) (map[string]any, error) {
	path, err := h.allowedPath(input)
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, fail(413, "配置文件过大")
	}
	return map[string]any{"content": string(body), "path": path}, nil
}
func (h *helper) activeRaw() (map[string]any, error) {
	return readProcessConfig(h.primary())
}

func readProcessConfig(proc *processInfo) (map[string]any, error) {
	if proc == nil || proc.ConfigPath == "" || proc.ConfigPath == "." {
		return nil, fail(409, "无法定位 Mihomo 启动配置")
	}
	info, err := os.Stat(proc.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("无法读取 Mihomo 启动配置 %s: %w", proc.ConfigPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fail(409, "Mihomo 启动配置路径不是普通文件，请检查外部 Core 的启动参数")
	}
	body, err := os.ReadFile(proc.ConfigPath)
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": string(body), "path": proc.ConfigPath, "pid": proc.PID, "mode": map[bool]string{true: "managed", false: "external"}[proc.Managed]}, nil
}
func (h *helper) proxyGroupOrder() (map[string]any, error) {
	active, err := h.activeRaw()
	if err != nil {
		return map[string]any{"configPath": nil, "order": []string{}}, nil
	}
	raw, _ := active["content"].(string)
	order := []string{}
	in := false
	indent := 0
	for _, line := range strings.Split(raw, "\n") {
		spaces := len(line) - len(strings.TrimLeft(line, " "))
		trim := strings.TrimSpace(line)
		if trim == "proxy-groups:" {
			in = true
			indent = spaces
			continue
		}
		if in && trim != "" && spaces <= indent {
			break
		}
		if in && strings.HasPrefix(trim, "- name:") {
			name := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trim, "- name:")), "\"'")
			if name != "" {
				order = append(order, name)
			}
		}
	}
	return map[string]any{"ok": true, "configPath": active["path"], "order": order}, nil
}

func (h *helper) validateConfig(ctx context.Context, candidate, target string) error {
	proc := h.primary()
	binary := h.config.managedCore
	dir := filepath.Dir(target)
	if proc != nil && proc.Exe != "" {
		binary = proc.Exe
		if proc.ConfigDir != "" {
			dir = proc.ConfigDir
		}
	}
	if _, err := os.Stat(binary); err != nil {
		return nil
	}
	command := exec.CommandContext(ctx, binary, "-t", "-d", dir, "-f", candidate)
	out, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Mihomo 配置校验失败: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
func (h *helper) prepareConfig(ctx context.Context, content string) (map[string]any, error) {
	return h.prepareConfigCandidate(ctx, content, true, false)
}

func (h *helper) prepareConfigCandidate(ctx context.Context, content string, validate, validationRequired bool) (map[string]any, error) {
	if strings.TrimSpace(content) == "" || len(content) > maxBody || strings.IndexByte(content, 0) >= 0 {
		return nil, fail(400, "配置内容无效")
	}
	proc := h.primary()
	target := h.config.managedConfig
	if proc != nil && proc.ConfigPath != "" {
		target = proc.ConfigPath
	}
	if !filepath.IsAbs(target) {
		return nil, fail(409, "Mihomo 配置路径不安全")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return nil, err
	}
	candidate := filepath.Join(filepath.Dir(target), fmt.Sprintf(".%s.cff.%d.new", filepath.Base(target), time.Now().UnixNano()))
	if err := os.WriteFile(candidate, []byte(content), 0o600); err != nil {
		return nil, err
	}
	if stat, statErr := os.Stat(target); statErr == nil {
		_ = os.Chmod(candidate, stat.Mode().Perm())
		if system, ok := stat.Sys().(*syscall.Stat_t); ok {
			_ = os.Chown(candidate, int(system.Uid), int(system.Gid))
		}
	} else if target == h.config.managedConfig {
		_ = os.Chmod(candidate, 0o640)
	}
	validationStarted := time.Now()
	validation := map[string]any{"ok": true, "method": "live-apply", "skipped": true, "durationMs": int64(0)}
	if validate {
		if err := h.validateConfig(ctx, candidate, target); err != nil {
			_ = os.Remove(candidate)
			return nil, err
		}
		validation = map[string]any{"ok": true, "method": "mihomo-test", "skipped": false, "durationMs": time.Since(validationStarted).Milliseconds()}
	}
	backup := ""
	mode, uid, gid := os.FileMode(0o640), -1, -1
	if _, err := os.Stat(target); err == nil {
		if stat, statErr := os.Stat(target); statErr == nil {
			mode = stat.Mode().Perm()
			if system, ok := stat.Sys().(*syscall.Stat_t); ok {
				uid, gid = int(system.Uid), int(system.Gid)
			}
		}
		backup = filepath.Join(h.config.backupDir, "config", filepath.Base(target)+"-"+safeStamp()+".yaml")
		if err = copyFile(target, backup, 0o600); err != nil {
			_ = os.Remove(candidate)
			return nil, err
		}
	}
	id := randomID()
	h.mu.Lock()
	h.transactions[id] = &transaction{Target: target, Backup: backup, Candidate: candidate, CreatedAt: time.Now(), Validated: validate, ValidationRequired: validationRequired, Mode: mode, UID: uid, GID: gid}
	h.mu.Unlock()
	return map[string]any{"ok": true, "txId": id, "target": target, "backup": nullable(backup), "validation": validation, "effectiveContent": content}, nil
}

func (h *helper) validateConfigTransaction(ctx context.Context, id string) (map[string]any, error) {
	h.mu.Lock()
	tx := h.transactions[id]
	if tx == nil {
		h.mu.Unlock()
		return nil, fail(409, "配置校验事务不存在或已失效")
	}
	candidate, target := tx.Candidate, tx.Target
	h.mu.Unlock()
	proc := h.primary()
	binary := h.config.managedCore
	if proc != nil && proc.Exe != "" {
		binary = proc.Exe
	}
	if _, err := os.Stat(binary); err != nil {
		return nil, fmt.Errorf("无法执行 Mihomo 配置校验，Core 不可用: %w", err)
	}

	started := time.Now()
	if err := h.validateConfig(ctx, candidate, target); err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.transactions[id] != tx {
		h.mu.Unlock()
		return nil, fail(409, "配置校验事务已发生变化")
	}
	tx.Validated = true
	h.mu.Unlock()
	return map[string]any{"ok": true, "method": "mihomo-test", "skipped": false, "durationMs": time.Since(started).Milliseconds()}, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func (h *helper) activateConfig(ctx context.Context, id string) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tx := h.transactions[id]
	if tx == nil {
		return nil, fail(409, "配置事务不存在或已失效")
	}
	if tx.ValidationRequired && !tx.Validated {
		return nil, fail(409, "配置尚未通过 Mihomo 校验，拒绝激活")
	}
	if tx.Offline && (tx.Target != h.config.managedConfig || h.readMode() != "managed" || h.primary() != nil) {
		return nil, fail(409, "Core 状态已变化，请刷新后重试保存")
	}
	if len(tx.UserSettings) > 0 {
		settings, previous, err := h.readUserSettings()
		if err != nil {
			return nil, err
		}
		mergeUserSettings(settings, tx.UserSettings)
		body, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return nil, err
		}
		if err = atomicWrite(h.userSettingsPath(), body, 0600); err != nil {
			return nil, err
		}
		tx.PreviousUserSettings = previous
		tx.UserSettingsApplied = true
	}
	if err := os.Rename(tx.Candidate, tx.Target); err != nil {
		return nil, errors.Join(err, h.restoreUserSettings(tx))
	}
	tx.Activation = "hot-reload"
	if tx.Offline {
		tx.Activation = "saved-only"
	}
	return map[string]any{"ok": true, "method": tx.Activation, "target": tx.Target}, nil
}
func (h *helper) rollbackConfig(ctx context.Context, id string) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tx := h.transactions[id]
	if tx == nil {
		return nil, fail(409, "配置回滚事务不存在或已失效")
	}
	if err := h.restoreUserSettings(tx); err != nil {
		return nil, err
	}
	if tx.Backup != "" {
		if err := copyFile(tx.Backup, tx.Target, 0o640); err != nil {
			return nil, err
		}
		_ = os.Chmod(tx.Target, tx.Mode)
		if tx.UID >= 0 {
			_ = os.Chown(tx.Target, tx.UID, tx.GID)
		}
	}
	_ = os.Remove(tx.Candidate)
	delete(h.transactions, id)
	return map[string]any{"ok": true, "target": tx.Target, "backup": nullable(tx.Backup)}, nil
}
func (h *helper) commitConfig(id string) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tx := h.transactions[id]
	if tx != nil {
		_ = os.Remove(tx.Candidate)
		delete(h.transactions, id)
	}
	return map[string]any{"ok": true}, nil
}

func replaceTopLevel(raw, key, rendered string) string {
	lines := strings.Split(raw, "\n")
	out := []string{}
	skip := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if skip && trim != "" && indent == 0 {
			skip = false
		}
		if !skip && indent == 0 && strings.HasPrefix(trim, key+":") {
			skip = true
			continue
		}
		if !skip {
			out = append(out, line)
		}
	}
	base := strings.TrimRight(strings.Join(out, "\n"), "\n")
	if rendered == "" {
		if base == "" {
			return ""
		}
		return base + "\n"
	}
	if base == "" {
		return rendered + "\n"
	}
	return base + "\n" + rendered + "\n"
}
func scalar(value any) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return strconv.Itoa(int(v))
	case string:
		return strconv.Quote(v)
	}
	return fmt.Sprint(value)
}

var yamlKeys = map[string]string{
	"enabled":      "enable",
	"enhancedMode": "enhanced-mode", "fakeIpRange": "fake-ip-range", "fakeIpRange6": "fake-ip-range6", "fakeIpFilterMode": "fake-ip-filter-mode",
	"preferH3": "prefer-h3", "respectRules": "respect-rules", "useHosts": "use-hosts", "useSystemHosts": "use-system-hosts", "directNameserverFollowPolicy": "direct-nameserver-follow-policy",
	"defaultNameserver": "default-nameserver", "proxyServerNameserver": "proxy-server-nameserver", "directNameserver": "direct-nameserver", "fakeIpFilter": "fake-ip-filter", "nameserverPolicy": "nameserver-policy",
	"autoRoute": "auto-route", "autoRedirect": "auto-redirect", "autoDetectInterface": "auto-detect-interface", "dnsHijack": "dns-hijack", "strictRoute": "strict-route", "routeExcludeAddress": "route-exclude-address",
}

func yamlKey(value string) string {
	if mapped := yamlKeys[value]; mapped != "" {
		return mapped
	}
	return value
}
func renderYAMLNode(value any, indent string) []string {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		lines := []string{}
		for _, key := range keys {
			child := typed[key]
			if list, ok := child.([]any); ok && len(list) == 0 {
				lines = append(lines, indent+yamlKey(key)+": []")
				continue
			}
			if object, ok := child.(map[string]any); ok && len(object) == 0 {
				lines = append(lines, indent+yamlKey(key)+": {}")
				continue
			}
			switch child.(type) {
			case map[string]any, []any:
				lines = append(lines, indent+yamlKey(key)+":")
				lines = append(lines, renderYAMLNode(child, indent+"  ")...)
			default:
				lines = append(lines, indent+yamlKey(key)+": "+scalar(child))
			}
		}
		return lines
	case []any:
		lines := []string{}
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				lines = append(lines, indent+"-")
				lines = append(lines, renderYAMLNode(object, indent+"  ")...)
			} else {
				lines = append(lines, indent+"- "+scalar(item))
			}
		}
		return lines
	default:
		return []string{indent + scalar(value)}
	}
}
func renderYAML(key string, value any) string {
	if _, complexMap := value.(map[string]any); complexMap {
		return strings.Join(append([]string{key + ":"}, renderYAMLNode(value, "  ")...), "\n")
	}
	if _, complexList := value.([]any); complexList {
		return strings.Join(append([]string{key + ":"}, renderYAMLNode(value, "  ")...), "\n")
	}
	return key + ": " + scalar(value)
}

func normalizeDNSForYAML(input map[string]any) (map[string]any, any) {
	dns := map[string]any{}
	for key, value := range input {
		dns[key] = value
	}
	hosts := dns["hosts"]
	delete(dns, "hosts")
	if policies, ok := dns["nameserverPolicy"].([]any); ok {
		mapping := map[string]any{}
		for _, item := range policies {
			if entry, ok := item.(map[string]any); ok {
				matcher, _ := entry["matcher"].(string)
				if matcher != "" {
					mapping[matcher] = entry["servers"]
				}
			}
		}
		dns["nameserverPolicy"] = mapping
	}
	fallback := map[string]any{"geoip": dns["fallbackGeoip"], "geoip-code": dns["fallbackGeoipCode"], "ipcidr": dns["fallbackIpCidr"], "domain": dns["fallbackDomain"]}
	for _, key := range []string{"fallbackGeoip", "fallbackGeoipCode", "fallbackIpCidr", "fallbackDomain"} {
		delete(dns, key)
	}
	dns["fallback-filter"] = fallback
	return dns, hosts
}
func hostMap(value any) map[string]any {
	result := map[string]any{}
	if items, ok := value.([]any); ok {
		for _, item := range items {
			if entry, ok := item.(map[string]any); ok {
				host, _ := entry["host"].(string)
				if host != "" {
					result[host] = entry["values"]
				}
			}
		}
	}
	return result
}
func normalizeTunForYAML(input map[string]any) (map[string]any, error) {
	tun := map[string]any{}
	for key, value := range input {
		tun[key] = value
	}
	if enabled, ok := tun["dnsHijack"].(bool); ok {
		if enabled {
			tun["dnsHijack"] = []any{"any:53"}
		} else {
			tun["dnsHijack"] = []any{}
		}
	}
	if value, exists := tun["routeExcludeAddress"]; exists {
		items, ok := value.([]any)
		if !ok {
			return nil, fail(400, "排除自定义网段必须是 CIDR 列表")
		}
		if len(items) > 128 {
			return nil, fail(400, "排除自定义网段最多支持 128 项")
		}
		normalized := make([]any, 0, len(items))
		seen := map[string]bool{}
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, fail(400, "排除自定义网段仅支持 IPv4/IPv6 CIDR")
			}
			prefix, err := netip.ParsePrefix(strings.TrimSpace(text))
			if err != nil {
				return nil, fail(400, fmt.Sprintf("无效的排除网段：%s", text))
			}
			canonical := prefix.Masked().String()
			if !seen[canonical] {
				seen[canonical] = true
				normalized = append(normalized, canonical)
			}
		}
		tun["routeExcludeAddress"] = normalized
	}
	return tun, nil
}

func replaceNestedBoolean(raw, section, key string, value bool) (string, error) {
	lines := strings.Split(raw, "\n")
	sectionLine := -1
	sectionEnd := len(lines)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(line)-len(strings.TrimLeft(line, " ")) != 0 || !strings.HasPrefix(trimmed, section+":") {
			continue
		}
		remainder := strings.TrimSpace(strings.TrimPrefix(trimmed, section+":"))
		if remainder != "" && !strings.HasPrefix(remainder, "#") {
			return "", fail(409, "TUN 快速切换暂不支持行内 YAML，请先在原始配置中将 tun 改为块状写法")
		}
		sectionLine = index
		for cursor := index + 1; cursor < len(lines); cursor++ {
			candidate := strings.TrimSpace(lines[cursor])
			indent := len(lines[cursor]) - len(strings.TrimLeft(lines[cursor], " "))
			if candidate != "" && indent == 0 {
				sectionEnd = cursor
				break
			}
		}
		break
	}

	valueText := "false"
	if value {
		valueText = "true"
	}
	if sectionLine < 0 {
		base := strings.TrimRight(raw, "\n")
		if base != "" {
			base += "\n"
		}
		return base + section + ":\n  " + key + ": " + valueText + "\n", nil
	}

	childIndent := -1
	for index := sectionLine + 1; index < sectionEnd; index++ {
		trimmed := strings.TrimSpace(lines[index])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(lines[index]) - len(strings.TrimLeft(lines[index], " "))
		if indent > 0 && (childIndent < 0 || indent < childIndent) {
			childIndent = indent
		}
	}
	if childIndent < 0 {
		childIndent = 2
	}
	for index := sectionLine + 1; index < sectionEnd; index++ {
		trimmed := strings.TrimSpace(lines[index])
		indent := len(lines[index]) - len(strings.TrimLeft(lines[index], " "))
		if indent != childIndent || !strings.HasPrefix(trimmed, key+":") {
			continue
		}
		comment := ""
		if offset := strings.Index(strings.TrimPrefix(trimmed, key+":"), "#"); offset >= 0 {
			remainder := strings.TrimPrefix(trimmed, key+":")
			comment = " " + strings.TrimSpace(remainder[offset:])
		}
		lines[index] = strings.Repeat(" ", childIndent) + key + ": " + valueText + comment
		return strings.Join(lines, "\n"), nil
	}

	insert := strings.Repeat(" ", childIndent) + key + ": " + valueText
	lines = append(lines, "")
	copy(lines[sectionLine+2:], lines[sectionLine+1:])
	lines[sectionLine+1] = insert
	return strings.Join(lines, "\n"), nil
}

func (h *helper) prepareTunToggle(ctx context.Context, enabled bool) (map[string]any, error) {
	proc := h.primary()
	if proc == nil {
		return nil, fail(409, "当前未检测到运行中的 Mihomo Core")
	}
	if enabled {
		capability := resolveTunCapability(proc, fileExists("/dev/net/tun"), os.Geteuid())
		if supported, _ := capability["supported"].(bool); !supported {
			return nil, fail(409, fmt.Sprint(capability["message"]))
		}
	}
	active, err := h.activeRaw()
	if err != nil {
		return nil, err
	}
	raw := active["content"].(string)
	previous := yamlNestedBoolean(raw, "tun", "enable", false)
	effective, err := replaceNestedBoolean(raw, "tun", "enable", enabled)
	if err != nil {
		return nil, err
	}
	prepared, err := h.prepareConfigCandidate(ctx, effective, false, true)
	if err != nil {
		return nil, err
	}
	prepared["enabled"] = enabled
	prepared["previousEnabled"] = previous
	h.attachUserSettings(prepared, map[string]any{"tun": map[string]any{"enable": enabled}})
	prepared["validation"] = map[string]any{"ok": false, "pending": true, "method": "mihomo-test"}
	return prepared, nil
}

// Network settings may edit the managed startup file while its Core is stopped.
func (h *helper) networkConfig() (map[string]any, error) {
	if h.readMode() == "managed" && h.primary() == nil {
		if err := h.ensureManagedConfig(); err != nil {
			return nil, err
		}
		result, err := readProcessConfig(&processInfo{ConfigPath: h.config.managedConfig, Managed: true})
		if err == nil {
			result["offline"] = true
		}
		return result, err
	}
	return h.activeRaw()
}

func (h *helper) updateNetwork(ctx context.Context, input map[string]any) (map[string]any, error) {
	active, err := h.networkConfig()
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"controller", "mixed", "socks", "http", "redir", "tproxy"} {
		value, exists := input[key]
		if !exists {
			continue
		}
		port, ok := value.(map[string]any)
		if !ok {
			return nil, fail(400, "端口设置格式无效")
		}
		enabled, _ := port["enabled"].(bool)
		if enabled || key == "controller" {
			n, ok := port["port"].(float64)
			if !ok || n < 1 || n > 65535 || n != float64(int(n)) {
				return nil, fail(400, "端口必须为 1–65535 的整数")
			}
		}
	}
	if active["offline"] == true {
		if _, err := os.Stat(h.config.managedCore); err != nil {
			return nil, fail(409, "托管内核尚未安装，无法校验配置，请先完成内核安装")
		}
	}
	raw := active["content"].(string)
	mapping := map[string]string{"controller": "external-controller", "mixed": "mixed-port", "socks": "socks-port", "http": "port", "redir": "redir-port", "tproxy": "tproxy-port", "allowLan": "allow-lan", "core": "", "tun": "tun", "dns": "dns"}
	settings := map[string]any{}
	for api, key := range mapping {
		value, ok := input[api]
		if !ok {
			continue
		}
		settings[api] = value
		if api == "controller" {
			port := int(numberFromMap(value, "port", 9090))
			raw = replaceTopLevel(raw, key, fmt.Sprintf("external-controller: 127.0.0.1:%d", port))
			continue
		}
		if key == "" {
			if core, ok := value.(map[string]any); ok {
				if item, ok := core["ipv6"]; ok {
					raw = replaceTopLevel(raw, "ipv6", renderYAML("ipv6", item))
				}
				if item, ok := core["unifiedDelay"]; ok {
					raw = replaceTopLevel(raw, "unified-delay", renderYAML("unified-delay", item))
				}
			}
			continue
		}
		if port, ok := value.(map[string]any); ok && strings.HasSuffix(key, "port") {
			enabled, _ := port["enabled"].(bool)
			if enabled {
				raw = replaceTopLevel(raw, key, renderYAML(key, port["port"]))
			} else {
				raw = replaceTopLevel(raw, key, "")
			}
			continue
		}
		if api == "dns" {
			if dns, ok := value.(map[string]any); ok {
				normalized, hosts := normalizeDNSForYAML(dns)
				raw = replaceTopLevel(raw, key, renderYAML(key, normalized))
				raw = replaceTopLevel(raw, "hosts", renderYAML("hosts", hostMap(hosts)))
				continue
			}
		}
		if api == "tun" {
			if tun, ok := value.(map[string]any); ok {
				normalized, normalizeErr := normalizeTunForYAML(tun)
				if normalizeErr != nil {
					return nil, normalizeErr
				}
				var patch map[string]any
				if err := yaml.Unmarshal([]byte(renderYAML(key, normalized)), &patch); err != nil {
					return nil, err
				}
				merged, err := configyaml.MergeOverrides([]byte(raw), patch)
				if err != nil {
					return nil, err
				}
				raw = string(merged)
				continue
			}
		}
		raw = replaceTopLevel(raw, key, renderYAML(key, value))
	}
	if err := validateNetworkPorts(raw, active["content"].(string), active["offline"] == true); err != nil {
		return nil, err
	}
	prepared, err := h.prepareConfig(ctx, raw)
	if err != nil {
		return nil, err
	}
	if active["offline"] == true {
		h.mu.Lock()
		if tx := h.transactions[prepared["txId"].(string)]; tx != nil {
			tx.Offline = true
		}
		h.mu.Unlock()
	}
	patch, patchErr := normalizeUserPatch(input)
	if patchErr != nil {
		_, _ = h.rollbackConfig(ctx, prepared["txId"].(string))
		return nil, patchErr
	}
	h.attachUserSettings(prepared, patch)
	prepared["previousContent"] = active["content"]
	prepared["settings"] = settings
	controller, _, _ := parseController(raw)
	prepared["controller"] = map[string]any{"clientUrl": "http://" + controller}
	return prepared, nil
}
func numberFromMap(value any, key string, fallback float64) float64 {
	if data, ok := value.(map[string]any); ok {
		if n, ok := data[key].(float64); ok && n >= 1 && n <= 65535 {
			return n
		}
	}
	return fallback
}
func (h *helper) networkStatus(ctx context.Context) (map[string]any, error) {
	active, err := h.networkConfig()
	if err != nil {
		return nil, err
	}
	raw := active["content"].(string)
	controller, _, mixed := parseController(raw)
	controllerPort := 9090
	if _, portText, ok := strings.Cut(controller, ":"); ok {
		if value, err := strconv.Atoi(portText); err == nil {
			controllerPort = value
		}
	}
	port := func(key string, fallback int) map[string]any {
		text, exists := yamlScalarValue(raw, key)
		value, parseErr := strconv.Atoi(text)
		if !exists || parseErr != nil || value <= 0 {
			return map[string]any{"enabled": false, "port": fallback}
		}
		return map[string]any{"enabled": true, "port": value}
	}
	settings := map[string]any{"controller": map[string]any{"enabled": true, "port": controllerPort}, "mixed": port("mixed-port", mixed), "socks": port("socks-port", 7898), "http": port("port", 7899), "redir": port("redir-port", 7895), "tproxy": port("tproxy-port", 7896), "allowLan": yamlBoolean(raw, "allow-lan", false), "core": map[string]any{"ipv6": yamlBoolean(raw, "ipv6", true), "unifiedDelay": yamlBoolean(raw, "unified-delay", false)}, "tun": map[string]any{"enabled": yamlNestedBoolean(raw, "tun", "enable", false), "stack": yamlNestedString(raw, "tun", "stack", "mixed"), "mtu": yamlNestedInteger(raw, "tun", "mtu", 1500), "routeExcludeAddress": yamlNestedStringList(raw, "tun", "route-exclude-address"), "autoRoute": yamlNestedBoolean(raw, "tun", "auto-route", true), "autoRedirect": yamlNestedBoolean(raw, "tun", "auto-redirect", true), "autoDetectInterface": yamlNestedBoolean(raw, "tun", "auto-detect-interface", true), "dnsHijack": yamlNestedBoolean(raw, "tun", "dns-hijack", true), "strictRoute": yamlNestedBoolean(raw, "tun", "strict-route", false)}}
	proc := h.primary()
	tunDevice := fileExists("/dev/net/tun")
	capability := resolveTunCapability(proc, tunDevice, os.Geteuid())
	return map[string]any{"ok": true, "configPath": active["path"], "settings": settings, "offline": active["offline"] == true, "tunCapability": capability}, nil
}

func resolveTunCapability(proc *processInfo, tunDevice bool, effectiveUID int) map[string]any {
	permission := proc != nil && (proc.Managed || effectiveUID == 0)
	supported := tunDevice && permission
	reason, message := "", "当前 Mihomo 具备 TUN 所需权限，可直接启用"
	if !tunDevice {
		reason, message = "tun-device-missing", "当前系统没有 /dev/net/tun，暂不能启用 TUN"
	} else if proc == nil {
		reason, message = "core-not-running", "当前未检测到运行中的 Mihomo Core"
	} else if !permission {
		reason, message = "permission-denied", "当前 Mihomo 不是 root 且没有可管理的 TUN 权限"
	}
	return map[string]any{"supported": supported, "tunDevice": tunDevice, "permission": permission, "reason": reason, "message": message}
}

func yamlScalarValue(raw, key string) (string, bool) {
	pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*([^#\r\n]+)`)
	match := pattern.FindStringSubmatch(raw)
	if len(match) != 2 {
		return "", false
	}
	return strings.Trim(strings.TrimSpace(match[1]), "\"'"), true
}
func yamlInteger(raw, key string, fallback int) int {
	if value, ok := yamlScalarValue(raw, key); ok {
		if number, err := strconv.Atoi(value); err == nil {
			return number
		}
	}
	return fallback
}
func yamlBoolean(raw, key string, fallback bool) bool {
	if value, ok := yamlScalarValue(raw, key); ok {
		switch strings.ToLower(value) {
		case "true", "yes", "on", "1":
			return true
		case "false", "no", "off", "0":
			return false
		}
	}
	return fallback
}
func yamlBlock(raw, key string) string {
	lines := strings.Split(raw, "\n")
	inside := false
	indent := 0
	out := []string{}
	for _, line := range lines {
		spaces := len(line) - len(strings.TrimLeft(line, " "))
		trim := strings.TrimSpace(line)
		if !inside && spaces == 0 && strings.HasPrefix(trim, key+":") {
			inside = true
			indent = spaces
			continue
		}
		if inside && trim != "" && spaces <= indent {
			break
		}
		if inside {
			out = append(out, strings.TrimPrefix(line, "  "))
		}
	}
	return strings.Join(out, "\n")
}
func yamlNestedString(raw, block, key, fallback string) string {
	if value, ok := yamlScalarValue(yamlBlock(raw, block), key); ok {
		return value
	}
	return fallback
}
func yamlNestedInteger(raw, block, key string, fallback int) int {
	return yamlInteger(yamlBlock(raw, block), key, fallback)
}
func yamlNestedBoolean(raw, block, key string, fallback bool) bool {
	return yamlBoolean(yamlBlock(raw, block), key, fallback)
}
func yamlNestedStringList(raw, block, key string) []any {
	lines := strings.Split(yamlBlock(raw, block), "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent != 0 || !strings.HasPrefix(trimmed, key+":") {
			continue
		}
		remainder := strings.TrimSpace(strings.TrimPrefix(trimmed, key+":"))
		if remainder == "[]" {
			return []any{}
		}
		if strings.HasPrefix(remainder, "[") && strings.HasSuffix(remainder, "]") {
			items := []any{}
			for _, item := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(remainder, "["), "]"), ",") {
				if value := strings.Trim(strings.TrimSpace(item), "\"'"); value != "" {
					items = append(items, value)
				}
			}
			return items
		}
		items := []any{}
		for _, child := range lines[index+1:] {
			childTrimmed := strings.TrimSpace(child)
			childIndent := len(child) - len(strings.TrimLeft(child, " "))
			if childTrimmed != "" && childIndent <= indent {
				break
			}
			if strings.HasPrefix(childTrimmed, "- ") {
				value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(childTrimmed, "- ")), "\"'")
				if value != "" {
					items = append(items, value)
				}
			}
		}
		return items
	}
	return []any{}
}
func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }
