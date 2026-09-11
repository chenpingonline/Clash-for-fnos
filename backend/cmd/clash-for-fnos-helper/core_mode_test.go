package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCoreModeDefaultsAndPersistence(t *testing.T) {
	for _, mode := range []string{"", "auto", "invalid", "external", "managed"} {
		t.Run(mode, func(t *testing.T) {
			h := testHelper(t)
			if mode != "" {
				if err := h.writeMode(mode); err != nil {
					t.Fatal(err)
				}
			}
			want := "managed"
			if mode == "external" {
				want = mode
			}
			if got := newHelper(h.config).readMode(); got != want {
				t.Fatalf("mode=%s want=%s", got, want)
			}
		})
	}
}

func TestAllPackageRequiresExplicitCoreDownload(t *testing.T) {
	h := testHelper(t)
	coreDir := filepath.Join(h.config.appDir, "core")
	if err := os.MkdirAll(coreDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(coreDir, "online-core.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	h = newHelper(h.config)
	if initial := h.bootstrapSnapshot(); initial["state"] != "download-required" || initial["delivery"] != "online" {
		t.Fatalf("all package initial state is not actionable: %#v", initial)
	}
	result, err := h.ensureBootstrap(context.Background(), false, "managed")
	if err != nil {
		t.Fatal(err)
	}
	if result["state"] != "download-required" || result["delivery"] != "online" || result["progress"] != 0 {
		t.Fatalf("unexpected bootstrap result: %#v", result)
	}
	if _, err := os.Stat(h.config.managedCore); !os.IsNotExist(err) {
		t.Fatalf("startup unexpectedly installed core: %v", err)
	}
}

func TestBootstrapStatusDoesNotWaitForCoreOperationLock(t *testing.T) {
	h := testHelper(t)
	h.setBootstrap(map[string]any{"state": "downloading", "progress": 42})
	h.mu.Lock()
	defer h.mu.Unlock()
	done := make(chan map[string]any, 1)
	go func() { done <- h.bootstrapSnapshot() }()
	select {
	case snapshot := <-done:
		if snapshot["state"] != "downloading" || snapshot["progress"] != 42 {
			t.Fatalf("unexpected snapshot: %#v", snapshot)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("bootstrap status blocked behind the core operation lock")
	}
}

func TestCoreDownloadProgressIsTheTransferredPercentage(t *testing.T) {
	tests := []struct {
		read, total int64
		want        int
	}{
		{0, 100, 0},
		{2, 100, 2},
		{26, 100, 26},
		{50, 100, 50},
		{100, 100, 100},
		{120, 100, 100},
		{10, 0, 0},
	}
	for _, test := range tests {
		if got := coreDownloadPercent(test.read, test.total); got != test.want {
			t.Fatalf("read=%d total=%d: got %d, want %d", test.read, test.total, got, test.want)
		}
	}
}

func TestCoreDownloadCanBeCancelled(t *testing.T) {
	h := testHelper(t)
	ctx, finish := h.beginCoreDownload(context.Background())
	defer finish()
	h.setBootstrap(map[string]any{"state": "downloading", "mode": "managed", "message": "正在下载官方 Mihomo Core（26%）", "progress": 26, "delivery": "online"})

	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/bootstrap/cancel", strings.NewReader("{}")))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"state":"canceling"`) {
		t.Fatalf("cancel status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-ctx.Done():
	case <-time.After(250 * time.Millisecond):
		t.Fatal("download context was not cancelled")
	}
	if snapshot := h.bootstrapSnapshot(); snapshot["progress"] != 26 || snapshot["message"] != "正在停止 Core 下载…" {
		t.Fatalf("unexpected cancel snapshot: %#v", snapshot)
	}
}

func TestCoreDownloadCancelRejectsIdleState(t *testing.T) {
	h := testHelper(t)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/bootstrap/cancel", strings.NewReader("{}")))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "没有正在下载") {
		t.Fatalf("cancel status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCoreRollbackRemovesAFirstTimeManualInstall(t *testing.T) {
	h := testHelper(t)
	if err := atomicWrite(h.config.managedCore, []byte("new core"), 0755); err != nil {
		t.Fatal(err)
	}
	h.coreTx["manual"] = &transaction{Target: h.config.managedCore, CreatedAt: time.Now()}
	if _, err := h.rollbackCore(context.Background(), "manual", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.config.managedCore); !os.IsNotExist(err) {
		t.Fatalf("first-time manual install was not removed by rollback: %v", err)
	}
}

func TestSelectedModeNeverFallsBackToOtherProcess(t *testing.T) {
	external := processInfo{PID: 11, Exe: "/usr/bin/mihomo"}
	managed := processInfo{PID: 12, Exe: "/app/mihomo", Managed: true}
	for _, mode := range []string{"managed", "external"} {
		got := selectProcess([]processInfo{external, managed}, mode)
		if got == nil || got.Managed != (mode == "managed") {
			t.Fatalf("%s selected %#v", mode, got)
		}
		other := external
		if mode == "external" {
			other = managed
		}
		if got := selectProcess([]processInfo{other}, mode); got != nil {
			t.Fatalf("%s fell back to %#v", mode, got)
		}
	}
}

func TestMissingOrDirectoryConfigIsActionable(t *testing.T) {
	for _, path := range []string{"", ".", t.TempDir()} {
		_, err := readProcessConfig(&processInfo{ConfigPath: cleanOptionalPath(path)})
		if err == nil || strings.Contains(err.Error(), "is a directory") {
			t.Fatalf("path=%q err=%v", path, err)
		}
	}
	if got := cleanOptionalPath(""); got != "" {
		t.Fatalf("empty path became %q", got)
	}
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte("mode: rule\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readProcessConfig(&processInfo{ConfigPath: file})
	if err != nil || got["content"] != "mode: rule\n" {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestBootstrapPortConflictPersistsManagedAndReportsError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	h := testHelper(t)
	if err := h.writeMode("external"); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(h.config.managedCore, []byte("#!/bin/sh\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	raw := fmt.Sprintf("external-controller: %s\nmixed-port: 0\n", listener.Addr())
	if err := atomicWrite(h.config.managedConfig, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = h.selectMode(context.Background(), "managed")
	if err == nil || !strings.Contains(err.Error(), "端口占用") {
		t.Fatalf("err=%v", err)
	}
	if h.readMode() != "managed" || h.bootstrapSnapshot()["state"] != "error" {
		t.Fatalf("mode=%s bootstrap=%v", h.readMode(), h.bootstrapSnapshot())
	}
	if _, err := os.Stat(h.config.managedPID); !os.IsNotExist(err) {
		t.Fatalf("should not launch on conflict: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.checkManagedPorts(); err != nil {
		t.Fatalf("released port: %v", err)
	}
}

func TestManagedEarlyExitIsNotReady(t *testing.T) {
	h := testHelper(t)
	if err := atomicWrite(h.config.managedCore, []byte("#!/bin/sh\nexit 42\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(h.config.managedConfig, []byte("external-controller: 127.0.0.1:0\nmixed-port: 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := h.ensureBootstrap(context.Background(), true, "")
	if err == nil || !strings.Contains(err.Error(), "启动后退出") || h.bootstrapSnapshot()["state"] != "error" {
		t.Fatalf("err=%v bootstrap=%v", err, h.bootstrapSnapshot())
	}
}

func TestStaleManagedPIDDoesNotStopUnrelatedProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	h := testHelper(t)
	if err := atomicWrite(h.config.managedPID, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	h.stopManaged()
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("unrelated process was stopped: %v", err)
	}
}

func TestCoreLifecycleRejectsExternalMode(t *testing.T) {
	h := testHelper(t)
	if err := h.writeMode("external"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/core/start-managed", "/core/stop-managed"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
		if w.Code != 409 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if h.readMode() != "external" {
		t.Fatal("lifecycle changed mode")
	}
}

func TestStoppedCoreRetainsModeAndHelperAvailability(t *testing.T) {
	h := testHelper(t)
	if err := h.writeMode("managed"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/core/stop-managed", strings.NewReader("{}")))
		if w.Code != 200 {
			t.Fatalf("stop: %d %s", w.Code, w.Body.String())
		}
	}
	if h.readMode() != "managed" || h.bootstrapSnapshot()["state"] != "stopped" {
		t.Fatal("stopped state not retained")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":true`) || !strings.Contains(w.Body.String(), `"canRestartService":false`) {
		t.Fatalf("helper unavailable: %s", w.Body.String())
	}
	if err := atomicWrite(h.config.managedCore, []byte("#!/bin/sh\nexit 42\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(h.config.managedConfig, []byte("external-controller: 127.0.0.1:0\nmixed-port: 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/core/start-managed", strings.NewReader("{}")))
	if w.Code < 400 || !strings.Contains(w.Body.String(), "启动后退出") || h.bootstrapSnapshot()["state"] != "error" {
		t.Fatalf("start failed without actionable error: %s", w.Body.String())
	}
}
