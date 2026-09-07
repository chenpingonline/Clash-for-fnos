package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultController = "http://127.0.0.1:9090"

type Settings struct {
	Controller         string `json:"controller"`
	Secret             string `json:"secret"`
	PersistSelections  bool   `json:"persistSelections"`
	HealthcheckURL     string `json:"healthcheckUrl"`
	HealthcheckTimeout int    `json:"healthcheckTimeout"`
}

type Client struct {
	SettingsFile string
	HTTPClient   *http.Client
}

type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

func (c *Client) LoadSettings() (Settings, error) {
	settings := Settings{
		Controller:         defaultController,
		PersistSelections:  true,
		HealthcheckURL:     "https://www.gstatic.com/generate_204",
		HealthcheckTimeout: 5000,
	}
	body, err := os.ReadFile(c.SettingsFile)
	if err != nil && !os.IsNotExist(err) {
		return Settings{}, fmt.Errorf("读取 Controller 设置失败: %w", err)
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &settings); err != nil {
			return Settings{}, fmt.Errorf("Controller 设置格式错误: %w", err)
		}
	}
	settings.Controller = strings.TrimRight(strings.TrimSpace(settings.Controller), "/")
	if settings.Controller == "" {
		settings.Controller = defaultController
	}
	if strings.TrimSpace(settings.HealthcheckURL) == "" {
		settings.HealthcheckURL = "https://www.gstatic.com/generate_204"
	}
	parsed, err := url.Parse(settings.Controller)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Settings{}, fmt.Errorf("控制器地址只支持 http/https")
	}
	if settings.HealthcheckTimeout < 1000 || settings.HealthcheckTimeout > 30000 {
		settings.HealthcheckTimeout = 5000
	}
	return settings, nil
}

func (c *Client) Do(ctx context.Context, method, apiPath string, body io.Reader, timeout time.Duration) (*http.Response, error) {
	settings, err := c.LoadSettings()
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(settings.Controller + "/")
	reference, err := url.Parse(strings.TrimPrefix(apiPath, "/"))
	if err != nil {
		return nil, fmt.Errorf("Mihomo API 路径无效: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, base.ResolveReference(reference).String(), body)
	if err != nil {
		return nil, err
	}
	if settings.Secret != "" {
		request.Header.Set("Authorization", "Bearer "+settings.Secret)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	requestClient := *client
	if timeout > 0 {
		requestClient.Timeout = timeout
	}
	response, err := requestClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求 Mihomo 失败: %w", err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	message := strings.TrimSpace(string(errorBody))
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(errorBody, &payload) == nil && payload.Message != "" {
		message = payload.Message
	}
	if message == "" {
		message = response.Status
	}
	return nil, &APIError{Status: response.StatusCode, Message: fmt.Sprintf("Mihomo %d: %s", response.StatusCode, message)}
}
