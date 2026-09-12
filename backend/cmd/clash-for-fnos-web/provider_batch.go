package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
)

const maxProviderBatchSize = 256

type providerBatchInput struct {
	Names []string `json:"names"`
}

type providerBatchResult struct {
	Name   string `json:"name"`
	Method string `json:"method,omitempty"`
	Error  string `json:"error,omitempty"`
}

func decodeProviderBatch(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var input providerBatchInput
	if !decodeJSONBody(w, r, &input) {
		return nil, false
	}
	seen := make(map[string]struct{}, len(input.Names))
	names := make([]string, 0, len(input.Names))
	for _, name := range input.Names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
		if len(names) > maxProviderBatchSize {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "批量更新最多支持 256 个 Provider"})
			return nil, false
		}
	}
	if len(names) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "没有可更新的 Provider"})
		return nil, false
	}
	return names, true
}

func (g *gateway) updateProxyProviders(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	names, ok := decodeProviderBatch(w, r)
	if !ok {
		return
	}
	results := make([]providerBatchResult, 0, len(names))
	success := 0
	for _, name := range names {
		escapedName, valid := escapedSegment(name)
		if !valid {
			results = append(results, providerBatchResult{Name: name, Error: "Provider 名称无效"})
			continue
		}
		err := mihomoRequest(r.Context(), client, http.MethodPut, "/providers/proxies/"+escapedName, nil, 30*time.Second)
		result := providerBatchResult{Name: name}
		if err != nil {
			result.Error = err.Error()
		} else {
			success++
		}
		results = append(results, result)
		if r.Context().Err() != nil {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": success, "failed": len(results) - success, "results": results})
}

func (g *gateway) updateRuleProviders(w http.ResponseWriter, r *http.Request, client *mihomo.Client) {
	names, ok := decodeProviderBatch(w, r)
	if !ok {
		return
	}
	results := make([]providerBatchResult, 0, len(names))
	success, fallback := 0, 0
	for _, name := range names {
		escapedName, valid := escapedSegment(name)
		if !valid {
			results = append(results, providerBatchResult{Name: name, Error: "Rule Provider 名称无效"})
			continue
		}
		result := providerBatchResult{Name: name}
		updated, err := g.updateRuleProvider(r.Context(), client, escapedName)
		if err != nil {
			result.Error = err.Error()
		} else {
			result.Method, _ = updated["method"].(string)
			success++
			if result.Method == "direct-fallback" {
				fallback++
			}
		}
		results = append(results, result)
		if r.Context().Err() != nil {
			break
		}
	}
	if success > 0 {
		g.rulesChanged()
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": success, "failed": len(results) - success, "fallback": fallback, "results": results})
}
