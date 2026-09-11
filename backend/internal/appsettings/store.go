package appsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type Store struct {
	File string
	mu   sync.Mutex
}

type Update struct {
	Controller           *string `json:"controller"`
	Secret               *string `json:"secret"`
	ClearSecret          bool    `json:"clearSecret"`
	ControllerAutoDetect *bool   `json:"controllerAutoDetect"`
	PersistSelections    *bool   `json:"persistSelections"`
	NotifyAppUpdates     *bool   `json:"notifyAppUpdates"`
	HealthcheckURL       *string `json:"healthcheckUrl"`
	HealthcheckTimeout   any     `json:"healthcheckTimeout"`
}

func (s *Store) WithDocument(operation func(map[string]any) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	document, err := s.load()
	if err != nil {
		return err
	}
	if err := operation(document); err != nil {
		return err
	}
	return s.save(document)
}

func (s *Store) ReadPublic() (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	document, err := s.load()
	if err != nil {
		return nil, err
	}
	return Public(document), nil
}

func (s *Store) ReadSecret() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	document, err := s.load()
	if err != nil {
		return "", err
	}
	secret, _ := document["secret"].(string)
	return secret, nil
}

func (s *Store) load() (map[string]any, error) {
	document := map[string]any{}
	body, err := os.ReadFile(s.File)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &document); err != nil {
			return nil, fmt.Errorf("设置格式错误: %w", err)
		}
	}
	return document, nil
}

func (s *Store) save(document map[string]any) error {
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.File), 0o700); err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.%d.tmp", s.File, os.Getpid())
	if err := os.WriteFile(temporary, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.File); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func boolValue(document map[string]any, key string, fallback bool) bool {
	value, ok := document[key].(bool)
	if !ok {
		return fallback
	}
	return value
}
func stringValue(document map[string]any, key, fallback string) string {
	value, ok := document[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func numberValue(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		number, err := strconv.Atoi(typed)
		if err == nil {
			return number
		}
	}
	return fallback
}

func Public(document map[string]any) map[string]any {
	secret, _ := document["secret"].(string)
	timeout := numberValue(document["healthcheckTimeout"], 5000)
	if timeout < 1000 || timeout > 30000 {
		timeout = 5000
	}
	return map[string]any{"controller": stringValue(document, "controller", "http://127.0.0.1:9090"), "hasSecret": secret != "", "controllerAutoDetect": boolValue(document, "controllerAutoDetect", true), "persistSelections": boolValue(document, "persistSelections", true), "notifyAppUpdates": boolValue(document, "notifyAppUpdates", true), "healthcheckUrl": stringValue(document, "healthcheckUrl", "https://www.gstatic.com/generate_204"), "healthcheckTimeout": timeout}
}

func Apply(document map[string]any, update Update) error {
	// This legacy preference was exposed without any runtime behavior. Remove it
	// the next time settings are saved instead of carrying misleading state.
	delete(document, "applyManagedConfigOnStart")
	if update.ControllerAutoDetect != nil {
		document["controllerAutoDetect"] = *update.ControllerAutoDetect
	}
	if update.PersistSelections != nil {
		document["persistSelections"] = *update.PersistSelections
	}
	if update.NotifyAppUpdates != nil {
		document["notifyAppUpdates"] = *update.NotifyAppUpdates
	}
	if update.Controller != nil {
		value := strings.TrimRight(strings.TrimSpace(*update.Controller), "/")
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("控制器地址只支持 http/https")
		}
		document["controller"] = value
	}
	if update.Secret != nil && *update.Secret != "" {
		document["secret"] = *update.Secret
	}
	if update.ClearSecret {
		document["secret"] = ""
	}
	if update.HealthcheckURL != nil {
		value := strings.TrimSpace(*update.HealthcheckURL)
		if value == "" {
			value = "https://www.gstatic.com/generate_204"
		}
		document["healthcheckUrl"] = value
	}
	if update.HealthcheckTimeout != nil {
		timeout := numberValue(update.HealthcheckTimeout, 5000)
		if timeout < 1000 {
			timeout = 1000
		}
		if timeout > 30000 {
			timeout = 30000
		}
		document["healthcheckTimeout"] = timeout
	}
	return nil
}

func SyncController(document, status map[string]any) {
	mode, _ := status["mode"].(string)
	auto := boolValue(document, "controllerAutoDetect", true)
	if mode == "managed" {
		if value, ok := status["managedController"].(string); ok && value != "" {
			document["controller"] = strings.TrimRight(value, "/")
		}
		if value, ok := status["managedSecret"].(string); ok {
			document["secret"] = value
		}
	}
	if mode == "external" && auto {
		if value, ok := status["detectedController"].(string); ok && value != "" {
			document["controller"] = strings.TrimRight(value, "/")
		}
		if present, _ := status["detectedSecretPresent"].(bool); present {
			if value, ok := status["detectedSecret"].(string); ok {
				document["secret"] = value
			}
		}
	}
}
