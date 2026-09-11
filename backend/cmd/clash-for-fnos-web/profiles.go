package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/privileged"
)

const (
	maxProfileSize        = 12 << 20
	defaultSubscriptionUA = "clash-verge/v2.4.3"
)

type profile struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	URL               string         `json:"url"`
	Type              string         `json:"type,omitempty"`
	SourcePath        string         `json:"sourcePath,omitempty"`
	AutoUpdate        bool           `json:"autoUpdate"`
	AutoApply         bool           `json:"autoApply"`
	IntervalMinutes   float64        `json:"intervalMinutes"`
	UpdatedAt         any            `json:"updatedAt,omitempty"`
	LastError         any            `json:"lastError,omitempty"`
	SubscriptionInfo  map[string]any `json:"subscriptionInfo,omitempty"`
	ProfileWebPageURL any            `json:"profileWebPageUrl,omitempty"`
	LastDownload      map[string]any `json:"lastDownload,omitempty"`
	ETag              string         `json:"etag,omitempty"`
	LastModified      string         `json:"lastModified,omitempty"`
	ContentSHA256     string         `json:"contentSha256,omitempty"`
}

type profileState struct {
	Current *string    `json:"current"`
	Items   []*profile `json:"items"`
}

type profileJob struct {
	ID        string         `json:"jobId"`
	ProfileID string         `json:"profileId"`
	Operation string         `json:"operation"`
	State     string         `json:"state"`
	Stage     string         `json:"stage"`
	Message   string         `json:"message"`
	Error     any            `json:"error"`
	Result    map[string]any `json:"result"`
	CreatedAt int64          `json:"createdAt"`
	UpdatedAt int64          `json:"updatedAt"`
}

type localCandidate struct {
	Token            string         `json:"token"`
	Path             string         `json:"path"`
	ActualPath       string         `json:"-"`
	Source           string         `json:"source"`
	Namespace        string         `json:"namespace"`
	AccessScope      string         `json:"accessScope"`
	Exists           bool           `json:"exists"`
	Readable         bool           `json:"readable"`
	PermissionDenied bool           `json:"permissionDenied,omitempty"`
	Size             int64          `json:"size"`
	Mtime            any            `json:"mtime"`
	RealPath         any            `json:"realPath"`
	ViaHelper        bool           `json:"viaHelper"`
	Process          map[string]any `json:"process"`
}

type localRuntime struct {
	Mode          string `json:"mode,omitempty"`
	Running       bool   `json:"running"`
	PID           int    `json:"pid,omitempty"`
	BinaryPath    string `json:"binaryPath,omitempty"`
	ConfigPath    string `json:"configPath,omitempty"`
	BinaryVersion string `json:"binaryVersion,omitempty"`
	Message       string `json:"message,omitempty"`
}

func newHexID(bytesCount int) string {
	body := make([]byte, bytesCount)
	if _, err := rand.Read(body); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(body)
}

func (g *gateway) readProfiles() (profileState, error) {
	state := profileState{Items: []*profile{}}
	body, err := os.ReadFile(g.config.profilesFile)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return state, err
	}
	if state.Items == nil {
		state.Items = []*profile{}
	}
	return state, nil
}

func (g *gateway) writeProfiles(state profileState) error {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(g.config.profilesFile, body)
}

func publicProfile(item *profile, current *string) map[string]any {
	typeName := item.Type
	if typeName == "" {
		typeName = "remote"
	}
	isCurrent := current != nil && *current == item.ID
	return map[string]any{
		"id": item.ID, "name": item.Name, "url": item.URL, "type": typeName,
		"sourcePath": nullableString(item.SourcePath), "autoUpdate": item.AutoUpdate,
		"autoApply": item.AutoApply, "intervalMinutes": item.IntervalMinutes,
		"updatedAt": nullableValue(item.UpdatedAt), "lastError": nullableValue(item.LastError),
		"subscriptionInfo": nullableMap(item.SubscriptionInfo), "profileWebPageUrl": nullableValue(item.ProfileWebPageURL),
		"userAgent": defaultSubscriptionUA, "lastDownload": nullableMap(item.LastDownload), "current": isCurrent,
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableValue(value any) any {
	if value == nil {
		return nil
	}
	return value
}
func nullableMap(value map[string]any) any {
	if value == nil {
		return nil
	}
	return value
}

func findProfile(state *profileState, id string) *profile {
	for _, item := range state.Items {
		if item.ID == id {
			return item
		}
	}
	return nil
}

func (g *gateway) handleProfilesAPI(w http.ResponseWriter, r *http.Request, requestPath string) bool {
	if strings.HasPrefix(requestPath, "/api/local-config/") {
		return g.handleLocalConfigAPI(w, r, requestPath)
	}
	if requestPath == "/api/profiles" && r.Method == http.MethodGet {
		g.profileMu.Lock()
		defer g.profileMu.Unlock()
		state, err := g.readProfiles()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return true
		}
		items := make([]map[string]any, 0, len(state.Items))
		for _, item := range state.Items {
			items = append(items, publicProfile(item, state.Current))
		}
		writeJSON(w, 200, map[string]any{"current": state.Current, "items": items})
		return true
	}
	if requestPath == "/api/profiles" && r.Method == http.MethodPost {
		var body struct {
			Name, URL       string
			AutoUpdate      *bool   `json:"autoUpdate"`
			AutoApply       bool    `json:"autoApply"`
			IntervalMinutes float64 `json:"intervalMinutes"`
		}
		if !decodeJSONBody(w, r, &body) {
			return true
		}
		body.Name, body.URL = strings.TrimSpace(body.Name), strings.TrimSpace(body.URL)
		if body.Name == "" {
			writeJSON(w, 400, map[string]string{"error": "请输入订阅名称"})
			return true
		}
		if _, err := validateProfileURL(body.URL); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return true
		}
		item := &profile{ID: newHexID(6), Name: body.Name, URL: body.URL, Type: "remote", AutoUpdate: body.AutoUpdate == nil || *body.AutoUpdate, AutoApply: body.AutoApply, IntervalMinutes: body.IntervalMinutes}
		if item.IntervalMinutes == 0 {
			item.IntervalMinutes = 360
		}
		g.profileMu.Lock()
		state, err := g.readProfiles()
		if err == nil {
			state.Items = append(state.Items, item)
			err = g.writeProfiles(state)
		}
		if err == nil {
			_, _, err = g.updateProfileLocked(r.Context(), &state, item, false, nil)
		}
		if err != nil {
			item.LastError = err.Error()
			_ = g.writeProfiles(state)
		}
		response := publicProfile(item, state.Current)
		g.profileMu.Unlock()
		writeJSON(w, 201, response)
		return true
	}
	if requestPath == "/api/profiles/import" && r.Method == http.MethodPost {
		var body struct{ Name, Content string }
		if !decodeJSONBody(w, r, &body) {
			return true
		}
		if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Content) == "" {
			writeJSON(w, 400, map[string]string{"error": "名称和配置内容不能为空"})
			return true
		}
		if len(body.Content) > maxProfileSize || strings.IndexByte(body.Content, 0) >= 0 {
			writeJSON(w, 413, map[string]string{"error": "配置内容无效或过大"})
			return true
		}
		g.profileMu.Lock()
		defer g.profileMu.Unlock()
		state, err := g.readProfiles()
		item := &profile{ID: newHexID(6), Name: strings.TrimSpace(body.Name), Type: "local", UpdatedAt: time.Now().UnixMilli()}
		if err == nil {
			err = writeAtomicFile(filepath.Join(g.config.profileDir, item.ID+".yaml"), []byte(body.Content))
		}
		if err == nil {
			state.Items = append(state.Items, item)
			err = g.writeProfiles(state)
		}
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 201, publicProfile(item, state.Current))
		}
		return true
	}
	if strings.HasPrefix(requestPath, "/api/jobs/") && r.Method == http.MethodGet {
		id := strings.TrimPrefix(requestPath, "/api/jobs/")
		g.jobMu.Lock()
		job := g.profileJobs[id]
		var response profileJob
		if job != nil {
			response = snapshotProfileJob(job)
		}
		g.jobMu.Unlock()
		if job == nil {
			writeJSON(w, 404, map[string]string{"error": "应用任务不存在或已过期"})
		} else {
			writeJSON(w, 200, response)
		}
		return true
	}
	if !strings.HasPrefix(requestPath, "/api/profiles/") {
		return false
	}
	tail := strings.TrimPrefix(requestPath, "/api/profiles/")
	id, operation, hasOperation := strings.Cut(tail, "/")
	if id == "" || strings.Contains(id, "/") {
		return false
	}
	if hasOperation && r.Method == http.MethodPost && (operation == "update" || operation == "update-activate" || operation == "activate" || operation == "apply-system") {
		g.profileMu.Lock()
		state, err := g.readProfiles()
		item := findProfile(&state, id)
		if err == nil && item == nil {
			err = os.ErrNotExist
		}
		if err != nil {
			g.profileMu.Unlock()
			writeJSON(w, 404, map[string]string{"error": "配置不存在"})
			return true
		}
		if operation == "update-activate" && (state.Current == nil || *state.Current != item.ID) {
			g.profileMu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "只能更新并应用当前正在使用的订阅"})
			return true
		}
		if operation == "activate" {
			response := g.startProfileJobLocked(item.ID, operation)
			g.profileMu.Unlock()
			writeJSON(w, 202, response)
			return true
		}
		if operation == "update" || operation == "update-activate" {
			response := g.startProfileJobLocked(item.ID, operation)
			g.profileMu.Unlock()
			writeJSON(w, 202, response)
			return true
		}
		var system map[string]any
		system, err = g.activateProfileLocked(r.Context(), &state, item, true, nil)
		response := publicProfile(item, state.Current)
		g.profileMu.Unlock()
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
		} else if operation == "apply-system" {
			writeJSON(w, 200, map[string]any{"profile": response, "system": system})
		} else {
			writeJSON(w, 200, response)
		}
		return true
	}
	if hasOperation {
		return false
	}
	g.profileMu.Lock()
	defer g.profileMu.Unlock()
	state, err := g.readProfiles()
	item := findProfile(&state, id)
	if err != nil || item == nil {
		writeJSON(w, 404, map[string]string{"error": "配置不存在"})
		return true
	}
	if r.Method == http.MethodPatch {
		var patch map[string]any
		if !decodeJSONBody(w, r, &patch) {
			return true
		}
		if value, ok := patch["name"].(string); ok {
			item.Name = value
		}
		if value, ok := patch["url"].(string); ok {
			if _, err := validateProfileURL(value); err != nil && item.Type != "local" {
				writeJSON(w, 400, map[string]string{"error": err.Error()})
				return true
			}
			if item.URL != value {
				item.ETag, item.LastModified, item.ContentSHA256 = "", "", ""
			}
			item.URL = value
		}
		if value, ok := patch["autoUpdate"].(bool); ok {
			item.AutoUpdate = value
		}
		if value, ok := patch["autoApply"].(bool); ok {
			item.AutoApply = value
		}
		if value, ok := patch["intervalMinutes"].(float64); ok {
			if value < 0 {
				value = 0
			}
			item.IntervalMinutes = value
		}
		if err := g.writeProfiles(state); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 200, publicProfile(item, state.Current))
		}
		return true
	}
	if r.Method == http.MethodDelete {
		items := state.Items[:0]
		for _, candidate := range state.Items {
			if candidate.ID != id {
				items = append(items, candidate)
			}
		}
		state.Items = items
		if state.Current != nil && *state.Current == id {
			state.Current = nil
		}
		_ = os.Remove(filepath.Join(g.config.profileDir, id+".yaml"))
		if err := g.writeProfiles(state); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
		} else {
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
		return true
	}
	return false
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any) bool {
	body, ok := readLimitedBody(w, r, maxProfileSize)
	if !ok {
		return false
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeJSON(w, 400, map[string]string{"error": "JSON 格式错误"})
		return false
	}
	return true
}

func validateProfileURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("订阅地址只支持 http/https")
	}
	return u, nil
}

func (g *gateway) downloadProfile(ctx context.Context, item *profile) ([]byte, map[string]any, map[string]any, any, bool, error) {
	u, err := validateProfileURL(item.URL)
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("订阅重定向次数过多")
		}
		return nil
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", defaultSubscriptionUA)
	if item.ETag != "" {
		req.Header.Set("If-None-Match", item.ETag)
	}
	if item.LastModified != "" {
		req.Header.Set("If-Modified-Since", item.LastModified)
	}
	started := time.Now()
	response, err := client.Do(req)
	if err != nil {
		return nil, nil, nil, nil, false, fmt.Errorf("订阅下载失败: %w", err)
	}
	defer response.Body.Close()
	download := map[string]any{"method": "direct", "label": "直连", "status": response.StatusCode, "durationMs": time.Since(started).Milliseconds(), "redirects": 0, "userAgent": defaultSubscriptionUA, "proxyUrl": nil, "updatedAt": time.Now().UnixMilli()}
	if response.StatusCode == http.StatusNotModified {
		download["unchanged"] = true
		download["conditional"] = true
		return nil, download, nil, nil, true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, nil, nil, false, fmt.Errorf("订阅下载失败: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxProfileSize+1))
	if err != nil {
		return nil, nil, nil, nil, false, err
	}
	if len(body) > maxProfileSize {
		return nil, nil, nil, nil, false, errors.New("订阅配置过大")
	}
	if len(strings.TrimSpace(string(body))) == 0 || strings.IndexByte(string(body), 0) >= 0 {
		return nil, nil, nil, nil, false, errors.New("订阅返回为空或不是文本 YAML")
	}
	item.ETag = response.Header.Get("ETag")
	item.LastModified = response.Header.Get("Last-Modified")
	info := parseSubscriptionInfo(response.Header.Get("subscription-userinfo"))
	var webURL any
	if value := strings.TrimSpace(response.Header.Get("profile-web-page-url")); value != "" {
		webURL = value
	}
	return body, download, info, webURL, false, nil
}

func parseSubscriptionInfo(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := map[string]any{}
	for _, part := range strings.Split(raw, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		if number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
			out[strings.TrimSpace(key)] = number
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (g *gateway) updateProfileLocked(ctx context.Context, state *profileState, item *profile, allowAutoApply bool, stage func(string, string)) ([]byte, bool, error) {
	var content []byte
	var err error
	changed := true
	profilePath := filepath.Join(g.config.profileDir, item.ID+".yaml")
	previous, previousErr := os.ReadFile(profilePath)
	if item.Type == "" || item.Type == "remote" {
		if stage != nil {
			stage("download", "正在连接订阅服务器并下载配置…")
		}
		var download map[string]any
		var info map[string]any
		var webURL any
		var notModified bool
		content, download, info, webURL, notModified, err = g.downloadProfile(ctx, item)
		if err == nil {
			item.LastDownload = download
			if notModified {
				if previousErr != nil {
					err = errors.New("订阅返回 304，但本地配置不存在，请修改订阅地址后重试")
				} else {
					content, changed = previous, false
				}
			} else {
				item.SubscriptionInfo, item.ProfileWebPageURL = info, webURL
			}
		}
	} else {
		if stage != nil {
			stage("reading", "正在读取本地配置…")
		}
		content, err = os.ReadFile(profilePath)
	}
	if err == nil && stage != nil {
		stage("compare", "下载完成，正在检查配置是否变化…")
	}
	if err == nil && changed && previousErr == nil && sha256.Sum256(previous) == sha256.Sum256(content) {
		changed = false
		if item.LastDownload != nil {
			item.LastDownload["unchanged"] = true
		}
	}
	if err == nil && changed {
		if stage != nil {
			stage("persist", "正在以原子方式保存订阅配置…")
		}
		err = writeAtomicFile(profilePath, content)
	}
	if err == nil {
		digest := sha256.Sum256(content)
		item.ContentSHA256 = hex.EncodeToString(digest[:])
		if changed {
			item.UpdatedAt = time.Now().UnixMilli()
		}
		item.LastError = nil
		err = g.writeProfiles(*state)
	}
	if err == nil && changed && allowAutoApply && state.Current != nil && *state.Current == item.ID && item.AutoApply {
		_, err = g.activateProfileLocked(ctx, state, item, true, stage)
	}
	if err != nil {
		item.LastError = err.Error()
		_ = g.writeProfiles(*state)
	}
	return content, changed, err
}

func (g *gateway) activateProfileLocked(ctx context.Context, state *profileState, item *profile, syncStartup bool, stage func(string, string)) (map[string]any, error) {
	if stage != nil {
		stage("reading", "读取配置…")
	}
	content, err := os.ReadFile(filepath.Join(g.config.profileDir, item.ID+".yaml"))
	if errors.Is(err, os.ErrNotExist) && (item.Type == "" || item.Type == "remote") {
		content, _, err = g.updateProfileLocked(ctx, state, item, false, stage)
	}
	if err != nil {
		return nil, err
	}
	g.networkMu.Lock()
	defer g.networkMu.Unlock()
	enabled, dns, err := g.dnsSettings()
	if err != nil {
		return nil, err
	}
	var composed map[string]any
	if err = g.helperJSON(ctx, http.MethodPost, "/config/compose", map[string]any{"content": string(content), "dnsOverrideEnabled": enabled, "dns": dns}, &composed, 10*time.Second); err != nil {
		return nil, err
	}
	effective, ok := composed["content"].(string)
	if !ok || strings.TrimSpace(effective) == "" {
		return nil, errors.New("合并用户设置后配置为空")
	}
	content = []byte(effective)
	if stage != nil {
		stage("applying", "准备进入安全应用流程…")
	}
	g.configMu.Lock()
	var result map[string]any
	if syncStartup {
		result, err = g.syncStartupConfigWithStage(ctx, content, stage)
	} else {
		err = g.saveAndApplyConfig(ctx, content)
	}
	g.configMu.Unlock()
	if err != nil {
		return nil, err
	}
	meta := map[string]any{}
	if body, readErr := os.ReadFile(g.config.configMetaFile); readErr == nil {
		_ = json.Unmarshal(body, &meta)
	}
	meta["source"], meta["sourceId"], meta["sourceName"] = "profile", item.ID, item.Name
	metaBody, _ := json.MarshalIndent(meta, "", "  ")
	_ = writeAtomicFile(g.config.configMetaFile, metaBody)
	current := item.ID
	state.Current = &current
	item.LastError = nil
	if err := g.writeProfiles(*state); err != nil {
		return nil, err
	}
	return result, nil
}

func snapshotProfileJob(job *profileJob) profileJob {
	snapshot := *job
	if job.Result != nil {
		snapshot.Result = make(map[string]any, len(job.Result))
		for key, value := range job.Result {
			snapshot.Result[key] = value
		}
	}
	return snapshot
}

func (g *gateway) startProfileJobLocked(profileID, operation string) profileJob {
	g.jobMu.Lock()
	if activeID := g.activeJobs[profileID]; activeID != "" {
		if job := g.profileJobs[activeID]; job != nil && job.State == "running" {
			response := snapshotProfileJob(job)
			g.jobMu.Unlock()
			return response
		}
	}
	now := time.Now().UnixMilli()
	message := "准备应用配置…"
	if operation == "update" {
		message = "准备更新订阅配置…"
	} else if operation == "update-activate" {
		message = "准备更新并应用当前订阅…"
	}
	job := &profileJob{ID: newHexID(10), ProfileID: profileID, Operation: operation, State: "running", Stage: "queued", Message: message, CreatedAt: now, UpdatedAt: now}
	g.profileJobs[job.ID], g.activeJobs[profileID] = job, job.ID
	response := snapshotProfileJob(job)
	g.jobMu.Unlock()
	go func() {
		g.profileMu.Lock()
		state, err := g.readProfiles()
		item := findProfile(&state, profileID)
		var result map[string]any
		if err == nil && item == nil {
			err = errors.New("配置不存在")
		}
		if err == nil && operation == "update-activate" && (state.Current == nil || *state.Current != profileID) {
			err = errors.New("当前使用的订阅已经变化，请刷新首页后重试")
		}
		if err == nil && (operation == "update" || operation == "update-activate") {
			var changed bool
			_, changed, err = g.updateProfileLocked(context.Background(), &state, item, false, func(stage, message string) { g.updateProfileJob(job.ID, stage, message) })
			if err == nil {
				result = map[string]any{"lastDownload": item.LastDownload, "unchanged": !changed}
			}
			if err == nil && changed && operation == "update-activate" {
				result, err = g.activateProfileLocked(context.Background(), &state, item, true, func(stage, message string) { g.updateProfileJob(job.ID, stage, message) })
				if result != nil {
					result["lastDownload"] = item.LastDownload
					result["unchanged"] = false
				}
			}
		} else if err == nil {
			result, err = g.activateProfileLocked(context.Background(), &state, item, true, func(stage, message string) { g.updateProfileJob(job.ID, stage, message) })
		}
		g.profileMu.Unlock()
		g.jobMu.Lock()
		defer g.jobMu.Unlock()
		job := g.profileJobs[job.ID]
		if job == nil {
			return
		}
		job.UpdatedAt = time.Now().UnixMilli()
		delete(g.activeJobs, profileID)
		if err != nil {
			failedMessage := "应用失败"
			if operation == "update" || operation == "update-activate" {
				failedMessage = "更新失败"
			}
			job.State, job.Stage, job.Message, job.Error = "failed", "failed", failedMessage, err.Error()
		} else {
			if result == nil {
				result = map[string]any{}
			}
			result["ok"] = true
			doneMessage := "配置已应用"
			if operation == "update" {
				doneMessage = "订阅配置已安全更新"
			} else if operation == "update-activate" {
				if result["unchanged"] == true {
					doneMessage = "订阅内容没有变化，无需重新应用"
				} else {
					doneMessage = "订阅已更新并应用"
				}
			}
			job.State, job.Stage, job.Message, job.Result = "done", "done", doneMessage, result
		}
		time.AfterFunc(10*time.Minute, func() { g.jobMu.Lock(); delete(g.profileJobs, job.ID); g.jobMu.Unlock() })
	}()
	return response
}

func (g *gateway) updateProfileJob(id, stage, message string) {
	g.jobMu.Lock()
	defer g.jobMu.Unlock()
	if job := g.profileJobs[id]; job != nil {
		job.Stage, job.Message, job.UpdatedAt = stage, message, time.Now().UnixMilli()
	}
}

func (g *gateway) runProfileScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.profileSchedulerTick(ctx)
		}
	}
}

func (g *gateway) profileSchedulerTick(ctx context.Context) {
	g.profileMu.Lock()
	defer g.profileMu.Unlock()
	state, err := g.readProfiles()
	if err != nil {
		return
	}
	now := time.Now().UnixMilli()
	for _, item := range state.Items {
		if !item.AutoUpdate || (item.Type != "" && item.Type != "remote") {
			continue
		}
		interval := item.IntervalMinutes
		if interval < 5 {
			interval = 5
		}
		updated, _ := toInt64(item.UpdatedAt)
		if checked, ok := toInt64(item.LastDownload["updatedAt"]); ok && checked > updated {
			updated = checked
		}
		if updated > 0 && now-updated < int64(interval*60_000) {
			continue
		}
		_, _, _ = g.updateProfileLocked(ctx, &state, item, true, nil)
	}
}

func toInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case json.Number:
		n, e := v.Int64()
		return n, e == nil
	}
	return 0, false
}

func (g *gateway) authorizedRoots() []string {
	body, err := os.ReadFile(g.config.authorizedFile)
	raw := g.config.accessiblePaths
	if err == nil {
		raw = string(body)
	}
	seen := map[string]bool{}
	roots := []string{}
	for _, part := range strings.Split(raw, ":") {
		value := filepath.Clean(strings.TrimSpace(part))
		if value == "." || !filepath.IsAbs(value) || strings.IndexByte(value, 0) >= 0 || seen[value] {
			continue
		}
		seen[value] = true
		roots = append(roots, value)
	}
	return roots
}

func withinRoot(file, root string) bool {
	relative, err := filepath.Rel(root, file)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (g *gateway) allowedLocalPath(file string) (string, string, error) {
	file = filepath.Clean(strings.TrimSpace(file))
	if !filepath.IsAbs(file) || (!strings.HasSuffix(strings.ToLower(file), ".yaml") && !strings.HasSuffix(strings.ToLower(file), ".yml")) {
		return "", "", errors.New("请输入 NAS 上的 YAML/YML 绝对路径")
	}
	if file == "/proc" || strings.HasPrefix(file, "/proc/") || file == "/sys" || strings.HasPrefix(file, "/sys/") || file == "/dev" || strings.HasPrefix(file, "/dev/") {
		return "", "", errors.New("不能手动读取该系统路径")
	}
	for _, root := range g.authorizedRoots() {
		if withinRoot(file, root) {
			return file, "authorized", nil
		}
	}
	for _, known := range systemConfigPaths {
		if file == known {
			return file, "system", nil
		}
	}
	return "", "", errors.New("该文件不在 fnOS 已授权目录中")
}

var systemConfigPaths = []string{"/etc/mihomo/config.yaml", "/etc/clash/config.yaml", "/usr/local/etc/mihomo/config.yaml", "/usr/local/etc/clash/config.yaml", "/opt/mihomo/config.yaml", "/var/lib/mihomo/config.yaml", "/root/.config/mihomo/config.yaml", "/root/.config/clash/config.yaml"}

func (g *gateway) inspectLocal(ctx context.Context, file, source, scope string) localCandidate {
	candidate := localCandidate{Path: file, ActualPath: file, Source: source, Namespace: "host", AccessScope: scope}
	if scope == "system" {
		var info map[string]any
		err := (privileged.Client{SocketPath: g.config.privilegedSocket}).DoJSON(ctx, http.MethodPost, "/config/inspect-path", map[string]any{"path": file}, &info, 10*time.Second)
		if err == nil {
			candidate.Exists, _ = info["exists"].(bool)
			candidate.Readable, _ = info["readable"].(bool)
			candidate.PermissionDenied, _ = info["permissionDenied"].(bool)
			candidate.Size = int64(numberValue(info["size"]))
			candidate.Mtime = info["mtime"]
			candidate.RealPath = info["realPath"]
			candidate.ViaHelper = candidate.Exists && candidate.Readable
		}
	} else if stat, err := os.Stat(file); err == nil && stat.Mode().IsRegular() {
		candidate.Exists, candidate.Readable, candidate.Size, candidate.Mtime = true, true, stat.Size(), stat.ModTime().UnixMilli()
		if real, err := filepath.EvalSymlinks(file); err == nil {
			candidate.RealPath = real
		}
	} else if errors.Is(err, os.ErrPermission) {
		candidate.PermissionDenied = true
	}
	if candidate.Exists {
		identity := fmt.Sprintf("%v|%d|%v", candidate.RealPath, candidate.Size, candidate.Mtime)
		sum := sha256.Sum256([]byte(identity))
		candidate.Token = hex.EncodeToString(sum[:12])
		g.localScans[candidate.Token] = candidate
	}
	return candidate
}

func numberValue(value any) float64 {
	if number, ok := value.(float64); ok {
		return number
	}
	return 0
}

func scanYAML(root string, maxDepth int) []string {
	files := []string{}
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		depth := strings.Count(relative, string(filepath.Separator))
		if entry.IsDir() {
			if depth > maxDepth || entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == "@eaDir" {
				return filepath.SkipDir
			}
			return nil
		}
		lower := strings.ToLower(entry.Name())
		if len(files) < 60 && (strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")) {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func (g *gateway) discoverLocalRuntime(ctx context.Context) *localRuntime {
	var status map[string]any
	if err := (privileged.Client{SocketPath: g.config.privilegedSocket}).GetJSON(ctx, "/status", &status); err != nil {
		return nil
	}
	pid := int(numberValue(status["pid"]))
	runtime := &localRuntime{
		Mode:          stringValue(status["mode"]),
		Running:       pid > 0,
		PID:           pid,
		BinaryPath:    stringValue(status["binaryPath"]),
		ConfigPath:    stringValue(status["configPath"]),
		BinaryVersion: stringValue(status["binaryVersion"]),
	}
	if bootstrap, ok := status["bootstrap"].(map[string]any); ok {
		runtime.Message = stringValue(bootstrap["message"])
		if runtime.Message == "" {
			runtime.Message = stringValue(bootstrap["error"])
		}
	}
	return runtime
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func (g *gateway) discoverLocalConfigs(ctx context.Context) map[string]any {
	g.localScanMu.Lock()
	defer g.localScanMu.Unlock()
	g.localScans = make(map[string]localCandidate)
	candidates := []localCandidate{}
	seen := map[string]bool{}
	for _, file := range systemConfigPaths {
		candidate := g.inspectLocal(ctx, file, "常见路径", "system")
		if candidate.Exists || candidate.PermissionDenied {
			candidates = append(candidates, candidate)
			seen[file] = true
		}
	}
	roots := g.authorizedRoots()
	for _, root := range roots {
		for _, file := range scanYAML(root, 3) {
			if seen[file] {
				continue
			}
			seen[file] = true
			candidates = append(candidates, g.inspectLocal(ctx, file, "用户授权目录", "authorized"))
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Readable != candidates[j].Readable {
			return candidates[i].Readable
		}
		return candidates[i].Path < candidates[j].Path
	})
	return map[string]any{"runtime": g.discoverLocalRuntime(ctx), "processes": []any{}, "candidates": candidates, "authorizedPaths": roots, "scannedAt": time.Now().UnixMilli()}
}

func (g *gateway) readLocalCandidate(ctx context.Context, candidate localCandidate) ([]byte, error) {
	var body []byte
	if candidate.ViaHelper {
		var result map[string]any
		if err := (privileged.Client{SocketPath: g.config.privilegedSocket}).DoJSON(ctx, http.MethodPost, "/config/read-path", map[string]any{"path": candidate.ActualPath}, &result, 15*time.Second); err != nil {
			return nil, err
		}
		body = []byte(fmt.Sprint(result["content"]))
	} else {
		file, scope, err := g.allowedLocalPath(candidate.ActualPath)
		if err != nil || scope != candidate.AccessScope {
			return nil, errors.New("配置路径授权已失效")
		}
		body, err = os.ReadFile(file)
		if err != nil {
			return nil, err
		}
	}
	if len(body) == 0 || len(body) > maxProfileSize || strings.IndexByte(string(body), 0) >= 0 {
		return nil, errors.New("配置文件为空、过大或不是文本 YAML")
	}
	return body, nil
}

func (g *gateway) handleLocalConfigAPI(w http.ResponseWriter, r *http.Request, requestPath string) bool {
	if requestPath == "/api/local-config/discover" && r.Method == http.MethodGet {
		writeJSON(w, 200, g.discoverLocalConfigs(r.Context()))
		return true
	}
	if requestPath == "/api/local-config/check" && r.Method == http.MethodPost {
		var body struct{ Path string }
		if !decodeJSONBody(w, r, &body) {
			return true
		}
		file, scope, err := g.allowedLocalPath(body.Path)
		if err != nil {
			writeJSON(w, 403, map[string]string{"error": err.Error()})
			return true
		}
		g.localScanMu.Lock()
		candidate := g.inspectLocal(r.Context(), file, map[bool]string{true: "用户授权路径", false: "系统 Mihomo 路径"}[scope == "authorized"], scope)
		g.localScanMu.Unlock()
		if !candidate.Exists {
			writeJSON(w, 404, map[string]string{"error": "没有找到这个配置文件"})
		} else {
			writeJSON(w, 200, candidate)
		}
		return true
	}
	if requestPath != "/api/local-config/import" || r.Method != http.MethodPost {
		return false
	}
	var body struct {
		Token, Name string
		Apply       bool
	}
	if !decodeJSONBody(w, r, &body) {
		return true
	}
	g.localScanMu.Lock()
	candidate, ok := g.localScans[body.Token]
	g.localScanMu.Unlock()
	if !ok {
		writeJSON(w, 409, map[string]string{"error": "配置扫描结果已失效，请重新扫描"})
		return true
	}
	raw, err := g.readLocalCandidate(r.Context(), candidate)
	if err != nil {
		writeJSON(w, 403, map[string]string{"error": err.Error()})
		return true
	}
	g.profileMu.Lock()
	defer g.profileMu.Unlock()
	state, err := g.readProfiles()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return true
	}
	var item *profile
	for _, existing := range state.Items {
		if existing.Type == "local" && existing.SourcePath == candidate.Path {
			item = existing
			break
		}
	}
	if item == nil {
		item = &profile{ID: newHexID(6), Name: strings.TrimSpace(body.Name), Type: "local", SourcePath: candidate.Path}
		if item.Name == "" {
			item.Name = filepath.Base(filepath.Dir(candidate.Path))
			if item.Name == "." || item.Name == "/" {
				item.Name = "本机配置"
			}
		}
		state.Items = append(state.Items, item)
	}
	item.UpdatedAt, item.LastError = time.Now().UnixMilli(), nil
	if body.Name != "" {
		item.Name = strings.TrimSpace(body.Name)
	}
	if err = writeAtomicFile(filepath.Join(g.config.profileDir, item.ID+".yaml"), raw); err == nil && body.Apply {
		_, err = g.activateProfileLocked(r.Context(), &state, item, false, nil)
	} else if err == nil {
		err = g.writeProfiles(state)
	}
	if err == nil && !body.Apply {
		_ = g.backupConfig()
		err = writeAtomicFile(g.config.managedConfigFile, raw)
		meta := map[string]any{"source": "nas-local", "path": candidate.Path, "namespace": candidate.Namespace, "importedAt": time.Now().UnixMilli(), "appliedAt": nil, "active": false}
		metaBody, _ := json.MarshalIndent(meta, "", "  ")
		if err == nil {
			err = writeAtomicFile(g.config.configMetaFile, metaBody)
		}
	}
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
	} else {
		writeJSON(w, 200, map[string]any{"item": publicProfile(item, state.Current), "applied": body.Apply, "sourcePath": candidate.Path})
	}
	return true
}
