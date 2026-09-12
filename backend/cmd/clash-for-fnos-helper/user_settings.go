package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/configyaml"
	"gopkg.in/yaml.v3"
)

func (h *helper) userSettingsPath() string {
	return filepath.Join(h.config.etcDir, "core-user-settings.json")
}
func (h *helper) readUserSettings() (map[string]any, []byte, error) {
	raw, err := os.ReadFile(h.userSettingsPath())
	if os.IsNotExist(err) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var settings map[string]any
	if err = json.Unmarshal(raw, &settings); err != nil {
		return nil, nil, fmt.Errorf("用户内核设置无法读取: %w", err)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	return settings, raw, nil
}
func mergeUserSettings(base, patch map[string]any) {
	for key, value := range patch {
		if key == "tun" {
			old, _ := base[key].(map[string]any)
			if old == nil {
				old = map[string]any{}
			}
			changes, _ := value.(map[string]any)
			for k, v := range changes {
				old[k] = v
			}
			base[key] = old
		} else {
			base[key] = value
		}
	}
}
func (h *helper) attachUserSettings(prepared map[string]any, patch map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if tx := h.transactions[prepared["txId"].(string)]; tx != nil {
		tx.UserSettings = patch
	}
}
func normalizeUserPatch(input map[string]any) (map[string]any, error) {
	patch := map[string]any{}
	ports := map[string]string{"controller": "external-controller", "mixed": "mixed-port", "socks": "socks-port", "http": "port", "redir": "redir-port", "tproxy": "tproxy-port"}
	for key, value := range input {
		if target, ok := ports[key]; ok {
			item, _ := value.(map[string]any)
			port := item["port"]
			enabled, _ := item["enabled"].(bool)
			if key == "controller" {
				patch[target] = fmt.Sprintf("127.0.0.1:%.0f", port)
			} else if enabled {
				patch[target] = port
			} else {
				patch[target] = 0
			}
		} else if key == "allowLan" {
			patch["allow-lan"] = value
		} else if key == "core" {
			if core, ok := value.(map[string]any); ok {
				for k, v := range core {
					if k == "ipv6" {
						patch[k] = v
					}
					if k == "unifiedDelay" {
						patch["unified-delay"] = v
					}
				}
			}
		} else if key == "tun" {
			item, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("TUN 设置格式无效")
			}
			tun, err := normalizeTunForYAML(item)
			if err != nil {
				return nil, err
			}
			var parsed map[string]any
			if err = yaml.Unmarshal([]byte(renderYAML("tun", tun)), &parsed); err != nil {
				return nil, err
			}
			patch["tun"] = parsed["tun"]
		}
	}
	return patch, nil
}
func (h *helper) composeUserSettings(input map[string]any) (map[string]any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	settings, _, err := h.readUserSettings()
	if err != nil {
		return nil, err
	}
	if enabled, _ := input["dnsOverrideEnabled"].(bool); enabled {
		if dns, ok := input["dns"].(map[string]any); ok {
			normalized, hosts := normalizeDNSForYAML(dns)
			var parsed map[string]any
			if err = yaml.Unmarshal([]byte(renderYAML("dns", normalized)), &parsed); err != nil {
				return nil, err
			}
			settings["dns"] = parsed["dns"]
			settings["hosts"] = hostMap(hosts)
		}
	}
	content, err := configyaml.MergeOverrides([]byte(stringField(input, "content")), settings)
	if err != nil {
		return nil, fmt.Errorf("合并用户设置失败: %w", err)
	}
	return map[string]any{"content": string(content)}, nil
}

func (h *helper) restoreUserSettings(tx *transaction) error {
	if !tx.UserSettingsApplied {
		return nil
	}
	var err error
	if tx.PreviousUserSettings == nil {
		err = os.Remove(h.userSettingsPath())
		if os.IsNotExist(err) {
			err = nil
		}
	} else {
		err = atomicWrite(h.userSettingsPath(), tx.PreviousUserSettings, 0600)
	}
	if err == nil {
		tx.UserSettingsApplied = false
	}
	return err
}

// Mode uses the same config transaction and override store as network settings.
func (h *helper) prepareRuntimeMode(ctx context.Context, mode string) (map[string]any, error) {
	if mode != "rule" && mode != "global" && mode != "direct" {
		return nil, fail(400, "运行模式无效")
	}
	active, err := h.networkConfig()
	if err != nil {
		return nil, err
	}
	raw, err := configyaml.MergeOverrides([]byte(stringField(active, "content")), map[string]any{"mode": mode})
	if err != nil {
		return nil, err
	}
	prepared, err := h.prepareConfigCandidate(ctx, string(raw), true, false)
	if err != nil {
		return nil, err
	}
	h.attachUserSettings(prepared, map[string]any{"mode": mode})
	return prepared, nil
}
