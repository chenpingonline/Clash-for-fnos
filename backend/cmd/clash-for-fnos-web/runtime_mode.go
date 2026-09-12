package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/chenpingonline/Clash-for-fnos/backend/internal/configyaml"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/mihomo"
	"github.com/chenpingonline/Clash-for-fnos/backend/internal/privileged"
)

func (g *gateway) updateRuntimeMode(ctx context.Context, live *mihomo.Client, mode string) error {
	g.networkMu.Lock()
	defer g.networkMu.Unlock()
	g.configMu.Lock()
	defer g.configMu.Unlock()
	client, err := live.Snapshot()
	if err != nil {
		return err
	}
	current, err := mihomoJSON(ctx, client, "/configs")
	if err != nil {
		return err
	}
	previousMode, _ := current["mode"].(string)
	if previousMode != "rule" && previousMode != "global" && previousMode != "direct" {
		return errors.New("无法确认原运行模式")
	}
	helper := privileged.Client{SocketPath: g.config.privilegedSocket}
	var prepared map[string]any
	if err = helper.DoJSON(ctx, http.MethodPost, "/config/mode", map[string]any{"mode": mode}, &prepared, time.Minute); err != nil {
		return fmt.Errorf("准备运行模式失败: %w", err)
	}
	txID, _ := prepared["txId"].(string)
	previous, readErr := os.ReadFile(g.config.managedConfigFile)
	rollback := func(cause error) error {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		diskErr := helper.DoJSON(cleanup, http.MethodPost, "/config/rollback", map[string]any{"txId": txID}, nil, 20*time.Second)
		if diskErr != nil {
			cause = errors.Join(cause, fmt.Errorf("模式配置回滚失败: %w", diskErr))
		}
		if liveErr := patchRuntimeMode(cleanup, client, previousMode); liveErr != nil {
			cause = errors.Join(cause, fmt.Errorf("运行模式回滚失败: %w", liveErr))
		}
		return cause
	}
	if err = helper.DoJSON(ctx, http.MethodPost, "/config/activate", map[string]any{"txId": txID}, nil, time.Minute); err != nil {
		return rollback(err)
	}
	if err = patchRuntimeMode(ctx, client, mode); err != nil {
		return rollback(err)
	}
	actual, err := mihomoJSON(ctx, client, "/configs")
	if err != nil {
		return rollback(err)
	}
	if actual["mode"] != mode {
		return rollback(errors.New("内核运行模式未按预期生效"))
	}
	if readErr == nil {
		effective, mergeErr := configyaml.MergeOverrides(previous, map[string]any{"mode": mode})
		if mergeErr != nil {
			return rollback(mergeErr)
		}
		if err = writeAtomicFile(g.config.managedConfigFile, effective); err != nil {
			return rollback(err)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return rollback(readErr)
	}
	if err = helper.DoJSON(ctx, http.MethodPost, "/config/commit", map[string]any{"txId": txID}, nil, 10*time.Second); err != nil {
		restoreFileSnapshot(g.config.managedConfigFile, previous, readErr == nil)
		return rollback(err)
	}
	return nil
}
