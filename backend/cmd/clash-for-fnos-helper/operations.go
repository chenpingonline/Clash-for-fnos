package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const proxyBegin = "# BEGIN CLASH-FOR-FNOS MANAGED PROXY"
const proxyEnd = "# END CLASH-FOR-FNOS MANAGED PROXY"
const legacyProxyBegin = "# >>> Clash for fnos proxy >>>"
const legacyProxyEnd = "# <<< Clash for fnos proxy <<<"

type proxySettings struct {
	Enabled         bool   `json:"enabled"`
	FollowMixedPort bool   `json:"followMixedPort"`
	Port            int    `json:"port"`
	NoProxy         string `json:"noProxy"`
	Targets         struct {
		Environment bool `json:"environment"`
		Profile     bool `json:"profile"`
		Bashrc      bool `json:"bashrc"`
	} `json:"targets"`
}

func defaultProxySettings() proxySettings {
	value := proxySettings{Enabled: true, FollowMixedPort: true, Port: 7890, NoProxy: "localhost,127.0.0.1,::1"}
	value.Targets.Environment, value.Targets.Profile, value.Targets.Bashrc = true, true, true
	return value
}
func (h *helper) readProxySettings() proxySettings {
	value := defaultProxySettings()
	body, _ := os.ReadFile(h.config.proxySettingsFile)
	_ = json.Unmarshal(body, &value)
	if value.Port < 1 || value.Port > 65535 {
		value.Port = 7890
	}
	if strings.TrimSpace(value.NoProxy) == "" {
		value.NoProxy = "localhost,127.0.0.1,::1"
	}
	return value
}
func sanitizeNoProxy(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00\"'") {
		return "", fail(400, "NO_PROXY 格式无效")
	}
	return value, nil
}
func proxyBlock(settings proxySettings, shell bool) string {
	port := settings.Port
	httpURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	socksURL := fmt.Sprintf("socks5h://127.0.0.1:%d", port)
	pairs := [][2]string{{"HTTP_PROXY", httpURL}, {"HTTPS_PROXY", httpURL}, {"ALL_PROXY", socksURL}, {"NO_PROXY", settings.NoProxy}, {"http_proxy", httpURL}, {"https_proxy", httpURL}, {"all_proxy", socksURL}, {"no_proxy", settings.NoProxy}}
	lines := []string{proxyBegin}
	for _, pair := range pairs {
		if shell {
			lines = append(lines, fmt.Sprintf("export %s=%q", pair[0], pair[1]))
		} else {
			lines = append(lines, fmt.Sprintf("%s=%q", pair[0], pair[1]))
		}
	}
	return strings.Join(append(lines, proxyEnd), "\n")
}
func stripProxyBlock(raw string) (string, error) {
	markerEnds := map[string]string{proxyBegin: proxyEnd, legacyProxyBegin: legacyProxyEnd}
	knownEnds := map[string]bool{proxyEnd: true, legacyProxyEnd: true}
	activeEnd := ""
	kept := strings.Builder{}
	for _, chunk := range strings.SplitAfter(raw, "\n") {
		marker := strings.TrimSpace(strings.TrimSuffix(chunk, "\n"))
		if end, isBegin := markerEnds[marker]; isBegin {
			if activeEnd != "" {
				return "", fail(409, "检测到嵌套的系统代理管理块，拒绝自动覆盖")
			}
			activeEnd = end
			continue
		}
		if knownEnds[marker] {
			if activeEnd == "" || marker != activeEnd {
				return "", fail(409, "检测到孤立或不匹配的系统代理结束标记，拒绝自动覆盖")
			}
			activeEnd = ""
			continue
		}
		if activeEnd == "" {
			kept.WriteString(chunk)
		}
	}
	if activeEnd != "" {
		return "", fail(409, "系统代理管理块未闭合，拒绝自动覆盖")
	}
	return kept.String(), nil
}
func withProxyBlock(raw, block string) (string, error) {
	clean, err := stripProxyBlock(raw)
	if err != nil {
		return "", err
	}
	clean = strings.TrimRight(clean, "\n")
	if clean != "" {
		clean += "\n\n"
	}
	return clean + block + "\n", nil
}

type proxyTarget struct {
	key, path string
	shell     bool
}

var proxyTargets = []proxyTarget{{"environment", "/etc/environment", false}, {"profile", "/etc/profile", true}, {"bashrc", "/etc/bash.bashrc", true}}

func selectedTarget(settings proxySettings, key string) bool {
	switch key {
	case "environment":
		return settings.Targets.Environment
	case "profile":
		return settings.Targets.Profile
	case "bashrc":
		return settings.Targets.Bashrc
	}
	return false
}
func (h *helper) applyProxySettings(settings proxySettings) ([]string, error) {
	changed := []string{}
	backupDir := filepath.Join(h.config.backupDir, "proxy-environment")
	for _, target := range proxyTargets {
		rawBytes, err := os.ReadFile(target.path)
		if errors.Is(err, os.ErrNotExist) {
			rawBytes = []byte{}
		} else if err != nil {
			return changed, err
		}
		raw := string(rawBytes)
		next := raw
		if settings.Enabled && selectedTarget(settings, target.key) {
			next, err = withProxyBlock(raw, proxyBlock(settings, target.shell))
		} else {
			next, err = stripProxyBlock(raw)
		}
		if err != nil {
			return changed, err
		}
		if next == raw {
			continue
		}
		if len(rawBytes) > 0 {
			_ = copyFile(target.path, filepath.Join(backupDir, strings.TrimPrefix(strings.ReplaceAll(target.path, "/", "_"), "_")+"-"+safeStamp()), 0o600)
		}
		mode := os.FileMode(0o644)
		if stat, e := os.Stat(target.path); e == nil {
			mode = stat.Mode().Perm()
		}
		if err = atomicWrite(target.path, []byte(next), mode); err != nil {
			return changed, err
		}
		changed = append(changed, target.path)
	}
	return changed, nil
}
func (h *helper) updateProxyEnvironment(body map[string]any) (map[string]any, error) {
	settings := h.readProxySettings()
	if value, ok := body["enabled"].(bool); ok {
		settings.Enabled = value
	}
	if value, ok := body["followMixedPort"].(bool); ok {
		settings.FollowMixedPort = value
	}
	if value, ok := body["port"].(float64); ok {
		settings.Port = int(value)
	}
	if settings.Port < 1 || settings.Port > 65535 {
		return nil, fail(400, "代理端口无效")
	}
	if value, ok := body["noProxy"].(string); ok {
		clean, err := sanitizeNoProxy(value)
		if err != nil {
			return nil, err
		}
		settings.NoProxy = clean
	}
	if targets, ok := body["targets"].(map[string]any); ok {
		if v, ok := targets["environment"].(bool); ok {
			settings.Targets.Environment = v
		}
		if v, ok := targets["profile"].(bool); ok {
			settings.Targets.Profile = v
		}
		if v, ok := targets["bashrc"].(bool); ok {
			settings.Targets.Bashrc = v
		}
	}
	if settings.FollowMixedPort {
		if raw, err := os.ReadFile(h.config.managedConfig); err == nil {
			_, _, settings.Port = parseController(string(raw))
		}
	}
	changed, err := h.applyProxySettings(settings)
	if err != nil {
		return nil, err
	}
	bodyJSON, _ := json.MarshalIndent(settings, "", "  ")
	if err = atomicWrite(h.config.proxySettingsFile, bodyJSON, 0o600); err != nil {
		return nil, err
	}
	status, _ := h.proxyEnvironment()
	status["operation"] = map[string]any{"changed": changed}
	return status, nil
}
func (h *helper) syncProxyEnvironment() (map[string]any, error) {
	settings := h.readProxySettings()
	return h.updateProxyEnvironment(map[string]any{"enabled": settings.Enabled, "followMixedPort": settings.FollowMixedPort, "port": settings.Port, "noProxy": settings.NoProxy, "targets": map[string]any{"environment": settings.Targets.Environment, "profile": settings.Targets.Profile, "bashrc": settings.Targets.Bashrc}})
}
func parseEnvLines(raw string) []map[string]any {
	out := []map[string]any{}
	keys := regexp.MustCompile(`(?i)^(?:export\s+)?(https?_proxy|all_proxy|no_proxy)\s*=\s*(.+)$`)
	for index, line := range strings.Split(raw, "\n") {
		match := keys.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) != 3 {
			continue
		}
		value := strings.Trim(strings.TrimSpace(match[2]), "\"'")
		if parsed, err := url.Parse(value); err == nil && parsed.User != nil {
			parsed.User = url.User("***")
			value = parsed.String()
		}
		out = append(out, map[string]any{"key": match[1], "value": value, "line": index + 1})
	}
	return out
}
func (h *helper) proxyEnvironment() (map[string]any, error) {
	files := []map[string]any{}
	for _, target := range proxyTargets {
		body, err := os.ReadFile(target.path)
		file := map[string]any{"path": target.path, "exists": err == nil, "readable": err == nil, "variables": []any{}}
		if err == nil {
			file["variables"] = parseEnvLines(string(body))
		} else if !errors.Is(err, os.ErrNotExist) {
			file["error"] = err.Error()
		}
		files = append(files, file)
	}
	settings := h.readProxySettings()
	return map[string]any{"ok": true, "files": files, "helperEnvironment": []any{}, "mihomoEnvironment": map[string]any{"pid": nil, "variables": []any{}}, "management": map[string]any{"active": settings.Enabled, "settings": settings}}, nil
}

type iconManifest struct {
	Default string                                   `json:"default"`
	Icons   []struct{ ID, Name, Description string } `json:"icons"`
}

func (h *helper) iconPaths() (string, string, string) {
	images := filepath.Join(h.config.appDir, "ui", "images")
	return images, filepath.Join(images, "icons"), filepath.Join(h.config.appDir, "server", "public", "icons")
}
func (h *helper) readIconManifest() (iconManifest, error) {
	_, presets, _ := h.iconPaths()
	var manifest iconManifest
	body, err := os.ReadFile(filepath.Join(presets, "manifest.json"))
	if err != nil {
		return manifest, err
	}
	err = json.Unmarshal(body, &manifest)
	return manifest, err
}
func validIconID(value string) bool {
	ok, _ := regexp.MatchString(`^[a-z0-9][a-z0-9-]{0,63}$`, value)
	return ok
}
func (h *helper) iconStatus() (map[string]any, error) {
	manifest, err := h.readIconManifest()
	if err != nil {
		return nil, err
	}
	selected := manifest.Default
	var stored struct {
		Selected string `json:"selected"`
	}
	if body, e := os.ReadFile(h.config.iconSettingsFile); e == nil && json.Unmarshal(body, &stored) == nil && validIconID(stored.Selected) {
		selected = stored.Selected
	}
	options := []map[string]any{}
	_, presets, _ := h.iconPaths()
	for _, item := range manifest.Icons {
		if !validIconID(item.ID) {
			continue
		}
		if !fileExists(filepath.Join(presets, item.ID+"_64.png")) || !fileExists(filepath.Join(presets, item.ID+"_256.png")) {
			continue
		}
		options = append(options, map[string]any{"id": item.ID, "name": item.Name, "description": item.Description, "preview": "/app/clash-for-fnos/icons/" + item.ID + "_256.png"})
	}
	return map[string]any{"ok": true, "selected": selected, "defaultId": manifest.Default, "options": options}, nil
}
func (h *helper) updateIcon(id string) (map[string]any, error) {
	if !validIconID(id) {
		return nil, fail(400, "软件图标标识无效")
	}
	manifest, err := h.readIconManifest()
	if err != nil {
		return nil, err
	}
	found := false
	for _, item := range manifest.Icons {
		if item.ID == id {
			found = true
		}
	}
	if !found {
		return nil, fail(404, "软件图标不存在")
	}
	images, presets, public := h.iconPaths()
	src64, src256 := filepath.Join(presets, id+"_64.png"), filepath.Join(presets, id+"_256.png")
	if err = copyFile(src64, filepath.Join(images, "icon_64.png"), 0o644); err != nil {
		return nil, err
	}
	if err = copyFile(src256, filepath.Join(images, "icon_256.png"), 0o644); err != nil {
		return nil, err
	}
	_ = os.MkdirAll(public, 0o755)
	_ = copyFile(src64, filepath.Join(public, id+"_64.png"), 0o644)
	_ = copyFile(src256, filepath.Join(public, id+"_256.png"), 0o644)
	installRoot := filepath.Join("/var/apps", env("TRIM_APPNAME", "clash-for-fnos"))
	_ = copyFile(src64, filepath.Join(installRoot, "ICON.PNG"), 0o644)
	_ = copyFile(src256, filepath.Join(installRoot, "ICON_256.PNG"), 0o644)
	body, _ := json.MarshalIndent(map[string]string{"selected": id}, "", "  ")
	if err = atomicWrite(h.config.iconSettingsFile, body, 0o600); err != nil {
		return nil, err
	}
	return h.iconStatus()
}

func safeStage(stage, root string) (string, error) {
	stage = filepath.Clean(stage)
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, stage)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fail(400, "Core 暂存路径不安全")
	}
	return stage, nil
}

type officialRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
		Digest             string `json:"digest"`
	} `json:"assets"`
}

func officialAssetNames(tag string) []string {
	if runtime.GOARCH == "amd64" {
		return []string{"mihomo-linux-amd64-v2-" + tag + ".gz", "mihomo-linux-amd64-" + tag + ".gz"}
	}
	return []string{"mihomo-linux-" + runtime.GOARCH + "-" + tag + ".gz"}
}

func fetchOfficialRelease(ctx context.Context, apiURL string) (officialRelease, error) {
	var release officialRelease
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	req.Header.Set("User-Agent", "Clash-for-fnos-helper/v"+version)
	req.Header.Set("Accept", "application/vnd.github+json")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return release, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return release, fmt.Errorf("查询官方 Mihomo Release 失败: HTTP %d", response.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&release)
	return release, err
}

func officialCoreAsset(ctx context.Context, tag string) (int64, string, error) {
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(tag) {
		return 0, "", fail(400, "Mihomo 版本号无效")
	}
	release, err := fetchOfficialRelease(ctx, "https://api.github.com/repos/MetaCubeX/mihomo/releases/tags/"+tag)
	if err != nil {
		return 0, "", err
	}
	for _, name := range officialAssetNames(tag) {
		for _, asset := range release.Assets {
			if asset.Name != name {
				continue
			}
			digest := strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:")
			if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) {
				return 0, "", errors.New("官方 Release 未提供有效 SHA-256")
			}
			return asset.Size, digest, nil
		}
	}
	return 0, "", fmt.Errorf("官方 Release 缺少 %s Core", runtime.GOARCH)
}

func (h *helper) downloadLatestCore(ctx context.Context) (string, error) {
	release, err := fetchOfficialRelease(ctx, "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest")
	if err != nil {
		return "", err
	}
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(release.TagName) {
		return "", errors.New("无法识别 Mihomo 最新版本")
	}
	var selected *struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
		Digest             string `json:"digest"`
	}
	for _, name := range officialAssetNames(release.TagName) {
		for index := range release.Assets {
			if release.Assets[index].Name == name {
				selected = &release.Assets[index]
				break
			}
		}
		if selected != nil {
			break
		}
	}
	if selected == nil {
		return "", fmt.Errorf("官方 Release 缺少 %s Core", runtime.GOARCH)
	}
	digest := strings.TrimPrefix(strings.ToLower(selected.Digest), "sha256:")
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) {
		return "", errors.New("官方 Release 未提供有效 SHA-256")
	}
	downloadURL, err := url.Parse(selected.BrowserDownloadURL)
	if err != nil || !(downloadURL.Hostname() == "github.com" || downloadURL.Hostname() == "objects.githubusercontent.com" || downloadURL.Hostname() == "release-assets.githubusercontent.com") {
		return "", errors.New("官方 Core 下载地址不安全")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL.String(), nil)
	req.Header.Set("User-Agent", "Clash-for-fnos-helper/v"+version)
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("下载官方 Mihomo Core 失败: HTTP %d", response.StatusCode)
	}
	compressed, err := io.ReadAll(io.LimitReader(response.Body, (80<<20)+1))
	if err != nil {
		return "", err
	}
	if len(compressed) > 80<<20 || (selected.Size > 0 && int64(len(compressed)) != selected.Size) {
		return "", errors.New("官方 Core 大小校验失败")
	}
	sum := sha256.Sum256(compressed)
	if hex.EncodeToString(sum[:]) != digest {
		return "", errors.New("官方 Core SHA-256 校验失败")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", err
	}
	binary, err := io.ReadAll(io.LimitReader(reader, 128<<20))
	_ = reader.Close()
	if err != nil || len(binary) == 0 {
		return "", errors.New("官方 Core 解压失败")
	}
	if err = atomicWrite(h.config.managedCore, binary, 0o755); err != nil {
		return "", err
	}
	return release.TagName, nil
}

func (h *helper) installCore(ctx context.Context, stage, expected string, restart bool) (map[string]any, error) {
	stage, err := safeStage(stage, h.config.stageDir)
	if err != nil {
		return nil, err
	}
	expectedSize, expectedDigest, err := officialCoreAsset(ctx, expected)
	if err != nil {
		return nil, err
	}
	compressed, err := os.ReadFile(stage)
	if err != nil {
		return nil, err
	}
	if len(compressed) == 0 || len(compressed) > 80<<20 || (expectedSize > 0 && int64(len(compressed)) != expectedSize) {
		return nil, fail(409, "官方内核压缩包大小不匹配")
	}
	compressedHash := sha256.Sum256(compressed)
	actualDigest := hex.EncodeToString(compressedHash[:])
	if actualDigest != expectedDigest {
		return nil, fail(409, "官方内核 SHA-256 校验失败")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	binary, err := io.ReadAll(io.LimitReader(reader, 128<<20))
	_ = reader.Close()
	if err != nil || len(binary) == 0 {
		return nil, errors.New("Mihomo Core 解压失败")
	}
	candidate := stage + ".verified"
	if err = atomicWrite(candidate, binary, 0o755); err != nil {
		return nil, err
	}
	defer os.Remove(candidate)
	out, err := exec.CommandContext(ctx, candidate, "-v").CombinedOutput()
	if err != nil || !strings.Contains(string(out), strings.TrimPrefix(expected, "v")) {
		return nil, fmt.Errorf("新内核版本不匹配: %s", strings.TrimSpace(string(out)))
	}
	proc := h.primary()
	target := h.config.managedCore
	if proc != nil && proc.Exe != "" {
		target = proc.Exe
	}
	if !filepath.IsAbs(target) {
		return nil, fail(409, "Mihomo 二进制路径不安全")
	}
	backup := ""
	oldVersion := ""
	if fileExists(target) {
		oldVersion = readVersion(target)
		backup = filepath.Join(h.config.backupDir, "core", "mihomo-"+strings.TrimPrefix(oldVersion, "v")+"-"+safeStamp())
		if err = copyFile(target, backup, 0o755); err != nil {
			return nil, err
		}
	}
	if err = copyFile(candidate, target, 0o755); err != nil {
		return nil, err
	}
	id := randomID()
	tx := &transaction{Target: target, Backup: backup, CreatedAt: time.Now(), Restart: restart}
	h.mu.Lock()
	h.coreTx[id] = tx
	h.mu.Unlock()
	restarted := false
	restartError := ""
	if restart && proc != nil && proc.Managed {
		h.stopManaged()
		_, err = h.startManaged()
		restarted = err == nil
		if err != nil {
			restartError = err.Error()
		}
	}
	return map[string]any{"ok": true, "txId": id, "target": target, "backup": nullable(backup), "oldVersion": oldVersion, "newVersion": expected, "versionOutput": string(out), "officialSha256": actualDigest, "restarted": restarted, "restartError": nullable(restartError), "restartRequired": !restarted}, nil
}
func (h *helper) rollbackCore(ctx context.Context, id string, restart bool) (map[string]any, error) {
	h.mu.Lock()
	tx := h.coreTx[id]
	h.mu.Unlock()
	if tx == nil {
		return nil, fail(409, "内核回滚事务不存在或已失效")
	}
	if tx.Backup != "" {
		if err := copyFile(tx.Backup, tx.Target, 0o755); err != nil {
			return nil, err
		}
	}
	restarted := false
	if restart && tx.Target == h.config.managedCore {
		h.stopManaged()
		_, err := h.startManaged()
		restarted = err == nil
	}
	h.mu.Lock()
	delete(h.coreTx, id)
	h.mu.Unlock()
	return map[string]any{"ok": true, "target": tx.Target, "backup": nullable(tx.Backup), "restarted": restarted}, nil
}
func (h *helper) commitCore(id string) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	tx := h.coreTx[id]
	if tx != nil {
		delete(h.coreTx, id)
		return map[string]any{"ok": true, "target": tx.Target, "backup": nullable(tx.Backup)}, nil
	}
	return map[string]any{"ok": true}, nil
}
