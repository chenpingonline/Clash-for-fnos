package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxGeoAssetSize = 64 << 20

type geoAssetDefinition struct {
	Key, Label, FileName, DefaultURL string
}

var geoAssets = []geoAssetDefinition{
	{Key: "geoip", Label: "GeoIP", FileName: "geoip.dat", DefaultURL: "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geoip.dat"},
	{Key: "geosite", Label: "GeoSite", FileName: "geosite.dat", DefaultURL: "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/geosite.dat"},
	{Key: "mmdb", Label: "Country MMDB", FileName: "Country.mmdb", DefaultURL: "https://testingcf.jsdelivr.net/gh/MetaCubeX/meta-rules-dat@release/country.mmdb"},
	{Key: "asn", Label: "ASN MMDB", FileName: "ASN.mmdb", DefaultURL: "https://github.com/xishang0128/geoip/releases/download/latest/GeoLite2-ASN.mmdb"},
}

func geoAssetByKey(key string) (geoAssetDefinition, bool) {
	for _, asset := range geoAssets {
		if asset.Key == key {
			return asset, true
		}
	}
	return geoAssetDefinition{}, false
}

func validateGeoAsset(asset geoAssetDefinition, body []byte, contentType string) error {
	if len(body) < 1024 {
		return errors.New("下载的 GEO 文件过小")
	}
	if strings.Contains(strings.ToLower(contentType), "text/html") || bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
		return errors.New("下载地址返回了网页而不是 GEO 文件")
	}
	if (asset.Key == "mmdb" || asset.Key == "asn") && !bytes.Contains(body, []byte("\xab\xcd\xefMaxMind.com")) {
		return errors.New("下载的 MMDB 文件格式无效")
	}
	return nil
}

func downloadGeoAssetFile(ctx context.Context, asset geoAssetDefinition, source, target string) error {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("GEO 下载地址必须是有效的 HTTPS 地址")
	}
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 || req.URL.Scheme != "https" || req.URL.Hostname() == "" || req.URL.User != nil {
			return errors.New("GEO 下载重定向地址不安全")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Clash-for-fnos-helper/v"+version)
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载 %s 失败: %w", asset.Label, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("下载 %s 失败: HTTP %d", asset.Label, response.StatusCode)
	}
	if response.ContentLength > maxGeoAssetSize {
		return fmt.Errorf("下载的 %s 超过 64 MB 限制", asset.Label)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGeoAssetSize+1))
	if err != nil {
		return fmt.Errorf("读取 %s 失败: %w", asset.Label, err)
	}
	if len(body) > maxGeoAssetSize {
		return fmt.Errorf("下载的 %s 超过 64 MB 限制", asset.Label)
	}
	if err = validateGeoAsset(asset, body, response.Header.Get("Content-Type")); err != nil {
		return fmt.Errorf("%s: %w", asset.Label, err)
	}
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode().IsRegular() {
			return nil
		}
		return errors.New("目标 GEO 路径不是普通文件")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	return atomicWrite(target, body, 0o644)
}

func (h *helper) downloadMissingGeoAsset(ctx context.Context, key string) (map[string]any, error) {
	asset, ok := geoAssetByKey(key)
	if !ok {
		return nil, fail(400, "不支持的 GEO 数据类型")
	}
	proc := h.primary()
	if proc == nil || !proc.Managed {
		return nil, fail(409, "仅 Manager 托管模式可下载 GEO 数据")
	}
	homeDir := proc.ConfigDir
	if homeDir == "" {
		homeDir = filepath.Dir(proc.ConfigPath)
	}
	raw, err := os.ReadFile(proc.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("读取当前配置失败: %w", err)
	}
	_, _, urls := geoSettingsFromConfig(string(raw))
	target := filepath.Join(homeDir, asset.FileName)
	if err = downloadGeoAssetFile(ctx, asset, urls[asset.Key], target); err != nil {
		return nil, err
	}
	status, err := h.geoStatus()
	if err != nil {
		return nil, err
	}
	status["downloaded"] = true
	status["assetKey"] = asset.Key
	return status, nil
}

func yamlNestedScalars(raw, key string) map[string]string {
	result := map[string]string{}
	lines := strings.Split(raw, "\n")
	inBlock, baseIndent := false, 0
	for _, line := range lines {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)
		if !inBlock {
			if indent == 0 && trimmed == key+":" {
				inBlock, baseIndent = true, indent
			}
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if indent <= baseIndent {
			break
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		value = strings.TrimSpace(value)
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, "'\"")
		}
		result[strings.TrimSpace(name)] = value
	}
	return result
}

func geoSettingsFromConfig(raw string) (bool, int, map[string]string) {
	autoUpdate := yamlBoolean(raw, "geo-auto-update", false)
	interval := 24
	if value, ok := yamlScalarValue(raw, "geo-update-interval"); ok {
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 {
			interval = parsed
		}
	}
	urls := yamlNestedScalars(raw, "geox-url")
	for _, asset := range geoAssets {
		if strings.TrimSpace(urls[asset.Key]) == "" {
			urls[asset.Key] = asset.DefaultURL
		}
	}
	return autoUpdate, interval, urls
}

func applyGeoSettings(raw string, autoUpdate bool, interval int) string {
	_, _, urls := geoSettingsFromConfig(raw)
	urlValues := map[string]any{}
	for key, value := range urls {
		urlValues[key] = value
	}
	raw = replaceTopLevel(raw, "geo-auto-update", renderYAML("geo-auto-update", autoUpdate))
	raw = replaceTopLevel(raw, "geo-update-interval", renderYAML("geo-update-interval", interval))
	return replaceTopLevel(raw, "geox-url", renderYAML("geox-url", urlValues))
}

func (h *helper) geoStatus() (map[string]any, error) {
	proc := h.primary()
	mode := h.readMode()
	homeDir, configPath := h.config.managedConfigDir, h.config.managedConfig
	managed := mode != "external"
	if proc != nil {
		managed = proc.Managed
		mode = map[bool]string{true: "managed", false: "external"}[managed]
		configPath = proc.ConfigPath
		if proc.ConfigDir != "" {
			homeDir = proc.ConfigDir
		} else if proc.ConfigPath != "" {
			homeDir = filepath.Dir(proc.ConfigPath)
		}
	}
	raw, _ := os.ReadFile(configPath)
	autoUpdate, interval, urls := geoSettingsFromConfig(string(raw))
	assets := make([]map[string]any, 0, len(geoAssets))
	for _, asset := range geoAssets {
		item := map[string]any{"key": asset.Key, "label": asset.Label, "fileName": asset.FileName, "present": false, "size": int64(0), "updatedAt": int64(0), "source": urls[asset.Key], "status": "missing"}
		if stat, err := os.Stat(filepath.Join(homeDir, asset.FileName)); err == nil && stat.Mode().IsRegular() {
			item["present"], item["size"], item["updatedAt"], item["status"] = true, stat.Size(), stat.ModTime().UnixMilli(), "ready"
		}
		assets = append(assets, item)
	}
	canUpdate := proc != nil && proc.Managed
	message := "GEO 数据由 Mihomo 管理"
	if !managed {
		message = "External 模式只读，请在外部 Mihomo 中管理 GEO 数据"
	} else if proc == nil {
		message = "Mihomo 尚未运行，启动后可更新 GEO 数据"
	}
	return map[string]any{"ok": true, "mode": mode, "readOnly": !managed, "canUpdate": canUpdate, "message": message, "configPath": configPath, "homeDir": homeDir, "settings": map[string]any{"autoUpdate": autoUpdate, "updateInterval": interval}, "assets": assets}, nil
}

func (h *helper) prepareGeoSettings(ctx context.Context, body map[string]any) (map[string]any, error) {
	proc := h.primary()
	if proc == nil || !proc.Managed {
		return nil, fail(409, "仅 Manager 托管模式可修改 GEO 更新设置")
	}
	autoUpdate, ok := body["autoUpdate"].(bool)
	if !ok {
		return nil, fail(400, "autoUpdate 必须是布尔值")
	}
	intervalValue, ok := body["updateInterval"].(float64)
	interval := int(intervalValue)
	if !ok || interval < 1 || interval > 720 || float64(interval) != intervalValue {
		return nil, fail(400, "更新周期必须是 1 到 720 小时的整数")
	}
	active, err := h.activeRaw()
	if err != nil {
		return nil, err
	}
	raw, _ := active["content"].(string)
	prepared, err := h.prepareConfig(ctx, applyGeoSettings(raw, autoUpdate, interval))
	if err != nil {
		return nil, err
	}
	h.attachUserSettings(prepared, map[string]any{"geo-auto-update": autoUpdate, "geo-update-interval": interval})
	prepared["settings"] = map[string]any{"autoUpdate": autoUpdate, "updateInterval": interval}
	return prepared, nil
}
