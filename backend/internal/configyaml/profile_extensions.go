package configyaml

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
	"gopkg.in/yaml.v3"
)

const (
	MaxExtensionSize = 1 << 20
	scriptTimeout    = 5 * time.Second
)

var ExtensionKinds = map[string]string{
	"rules":    "rules.yaml",
	"proxies":  "proxies.yaml",
	"groups":   "groups.yaml",
	"override": "override.yaml",
	"script":   "script.js",
}

type SequenceExtension struct {
	Prepend []any    `yaml:"prepend"`
	Append  []any    `yaml:"append"`
	Delete  []string `yaml:"delete"`
}

func DefaultExtension(kind string) string {
	switch kind {
	case "rules", "proxies", "groups":
		return "prepend: []\n\nappend: []\n\ndelete: []\n"
	case "override":
		return "# 在这里覆写配置；对象递归合并，数组会整体替换\n{}\n"
	case "script":
		return "// main 接收有效配置与订阅名称，必须返回配置对象\nfunction main(config, profileName) {\n  return config;\n}\n"
	default:
		return ""
	}
}

func ValidateExtension(kind, content string) error {
	if _, ok := ExtensionKinds[kind]; !ok {
		return errors.New("不支持的增强类型")
	}
	if len(content) > MaxExtensionSize {
		return errors.New("增强内容不能超过 1 MiB")
	}
	if kind == "script" {
		_, err := goja.Compile("profile-extension.js", content, false)
		if err != nil {
			return fmt.Errorf("脚本语法错误: %w", err)
		}
		return nil
	}
	if kind == "override" {
		var value map[string]any
		if err := yaml.Unmarshal([]byte(content), &value); err != nil {
			return fmt.Errorf("覆写配置 YAML 错误: %w", err)
		}
		return nil
	}
	var extension SequenceExtension
	if err := yaml.Unmarshal([]byte(content), &extension); err != nil {
		return fmt.Errorf("增强配置 YAML 错误: %w", err)
	}
	return nil
}

func ApplyProfileExtensions(raw []byte, profileName string, extensions map[string]string) ([]byte, error) {
	config, err := parseProfileConfig(raw)
	if err != nil {
		return nil, err
	}
	if err = applySequenceExtensions(config, extensions); err != nil {
		return nil, err
	}
	config, err = applyOverrideAndScript(config, profileName, extensions)
	if err != nil {
		return nil, err
	}
	return marshalProfileConfig(config)
}

// ApplyProfileExtensionChain follows Clash Verge's extension order:
// profile sequence editors, global override, global script, profile override,
// then profile script.
func ApplyProfileExtensionChain(raw []byte, profileName string, globalExtensions, profileExtensions map[string]string) ([]byte, error) {
	config, err := parseProfileConfig(raw)
	if err != nil {
		return nil, err
	}
	if err = applySequenceExtensions(config, profileExtensions); err != nil {
		return nil, err
	}
	config, err = applyOverrideAndScript(config, profileName, globalExtensions)
	if err != nil {
		return nil, fmt.Errorf("应用全局增强失败: %w", err)
	}
	config, err = applyOverrideAndScript(config, profileName, profileExtensions)
	if err != nil {
		return nil, fmt.Errorf("应用单配置增强失败: %w", err)
	}
	return marshalProfileConfig(config)
}

func parseProfileConfig(raw []byte) (map[string]any, error) {
	var config map[string]any
	if err := yaml.Unmarshal(raw, &config); err != nil {
		return nil, fmt.Errorf("解析订阅配置失败: %w", err)
	}
	if config == nil {
		return nil, errors.New("订阅配置必须是 YAML 对象")
	}
	return config, nil
}

func applySequenceExtensions(config map[string]any, extensions map[string]string) error {
	for _, item := range []struct{ kind, field string }{{"rules", "rules"}, {"proxies", "proxies"}, {"groups", "proxy-groups"}} {
		content := strings.TrimSpace(extensions[item.kind])
		if content == "" {
			continue
		}
		var extension SequenceExtension
		if err := yaml.Unmarshal([]byte(content), &extension); err != nil {
			return fmt.Errorf("%s 增强配置无效: %w", item.kind, err)
		}
		applySequence(config, item.field, extension)
		if item.kind == "proxies" {
			updateProxyGroupReferences(config, extension)
		}
	}
	return nil
}

func applyOverrideAndScript(config map[string]any, profileName string, extensions map[string]string) (map[string]any, error) {
	if content := strings.TrimSpace(extensions["override"]); content != "" && content != "{}" {
		var patch map[string]any
		if err := yaml.Unmarshal([]byte(content), &patch); err != nil {
			return nil, fmt.Errorf("覆写配置无效: %w", err)
		}
		deepMerge(config, patch)
	}
	if content := strings.TrimSpace(extensions["script"]); content != "" {
		var err error
		config, err = runScript(content, config, profileName)
		if err != nil {
			return nil, err
		}
	}
	return config, nil
}

func marshalProfileConfig(config map[string]any) ([]byte, error) {
	out, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("生成增强配置失败: %w", err)
	}
	return out, nil
}

func applySequence(config map[string]any, field string, extension SequenceExtension) {
	existing, _ := config[field].([]any)
	deleted := make(map[string]bool, len(extension.Delete))
	for _, name := range extension.Delete {
		deleted[name] = true
	}
	result := make([]any, 0, len(extension.Prepend)+len(existing)+len(extension.Append))
	result = append(result, extension.Prepend...)
	for _, value := range existing {
		if !deleted[sequenceIdentity(value)] {
			result = append(result, value)
		}
	}
	result = append(result, extension.Append...)
	config[field] = result
}

func sequenceIdentity(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if object, ok := value.(map[string]any); ok {
		name, _ := object["name"].(string)
		return name
	}
	return ""
}

func updateProxyGroupReferences(config map[string]any, extension SequenceExtension) {
	deleted := make(map[string]bool, len(extension.Delete))
	for _, name := range extension.Delete {
		deleted[name] = true
	}
	added := []string{}
	seenAdded := map[string]bool{}
	for _, value := range append(append([]any{}, extension.Prepend...), extension.Append...) {
		if name := sequenceIdentity(value); name != "" && !seenAdded[name] {
			seenAdded[name] = true
			added = append(added, name)
		}
	}
	groups, _ := config["proxy-groups"].([]any)
	inserted := false
	for _, value := range groups {
		group, ok := value.(map[string]any)
		if !ok {
			continue
		}
		members, hadMembers := group["proxies"].([]any)
		if hadMembers {
			kept := members[:0]
			for _, member := range members {
				name, _ := member.(string)
				if !deleted[name] {
					kept = append(kept, member)
				}
			}
			members = kept
		}
		kind, _ := group["type"].(string)
		addedHere := false
		if !inserted && len(added) > 0 && (strings.EqualFold(kind, "select") || strings.EqualFold(kind, "selector")) {
			merged := make([]any, 0, len(added)+len(members))
			seen := map[string]bool{}
			for _, name := range added {
				merged = append(merged, name)
				seen[name] = true
			}
			for _, member := range members {
				name, _ := member.(string)
				if name == "" || !seen[name] {
					merged = append(merged, member)
					seen[name] = true
				}
			}
			members, inserted, addedHere = merged, true, true
		}
		if hadMembers || addedHere {
			group["proxies"] = members
		}
	}
}

func deepMerge(target, patch map[string]any) {
	for key, value := range patch {
		if patchMap, ok := value.(map[string]any); ok {
			if targetMap, ok := target[key].(map[string]any); ok {
				deepMerge(targetMap, patchMap)
				continue
			}
		}
		target[key] = value
	}
}

func runScript(script string, config map[string]any, profileName string) (map[string]any, error) {
	if len(script) > MaxExtensionSize {
		return nil, errors.New("扩展脚本不能超过 1 MiB")
	}
	program, err := goja.Compile("profile-extension.js", script, false)
	if err != nil {
		return nil, fmt.Errorf("扩展脚本语法错误: %w", err)
	}
	vm := goja.New()
	timer := time.AfterFunc(scriptTimeout, func() { vm.Interrupt("脚本执行超过 5 秒") })
	defer timer.Stop()
	// Match Clash Verge's script contract: scripts receive a plain JSON-backed
	// JavaScript object. Passing Go maps and slices directly makes nested array
	// mutations such as group.proxies.push(...) fail to persist.
	configBody, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("准备扩展脚本失败: %w", err)
	}
	if err = vm.Set("__profileJSON", string(configBody)); err != nil {
		return nil, fmt.Errorf("准备扩展脚本失败: %w", err)
	}
	console := vm.NewObject()
	for _, level := range []string{"log", "info", "warn", "error", "debug", "table"} {
		if err = console.Set(level, func(goja.FunctionCall) goja.Value { return goja.Undefined() }); err != nil {
			return nil, fmt.Errorf("准备扩展脚本控制台失败: %w", err)
		}
	}
	if err = vm.Set("console", console); err != nil {
		return nil, fmt.Errorf("准备扩展脚本控制台失败: %w", err)
	}
	if _, err = vm.RunProgram(program); err != nil {
		return nil, fmt.Errorf("扩展脚本执行失败: %w", err)
	}
	mainValue := vm.Get("main")
	main, ok := goja.AssertFunction(mainValue)
	if !ok {
		return nil, errors.New("扩展脚本必须定义 main(config, profileName)")
	}
	configValue, err := vm.RunString("JSON.parse(__profileJSON)")
	if err != nil {
		return nil, fmt.Errorf("准备扩展脚本配置失败: %w", err)
	}
	result, err := main(goja.Undefined(), configValue, vm.ToValue(profileName))
	if err != nil {
		return nil, fmt.Errorf("扩展脚本执行失败: %w", err)
	}
	body, err := json.Marshal(result.Export())
	if err != nil {
		return nil, fmt.Errorf("扩展脚本返回值无法序列化: %w", err)
	}
	if len(body) > 10<<20 {
		return nil, errors.New("扩展脚本返回配置超过 10 MiB")
	}
	var output map[string]any
	if err = json.Unmarshal(body, &output); err != nil || output == nil {
		return nil, errors.New("扩展脚本 main 必须返回配置对象")
	}
	return output, nil
}
