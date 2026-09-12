package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProfileJobStreamPushesUpdatesUntilCompletion(t *testing.T) {
	gateway := newGateway(config{})
	now := time.Now().UnixMilli()
	gateway.profileJobs["job-1"] = &profileJob{ID: "job-1", State: "running", Stage: "queued", Message: "准备中", CreatedAt: now, UpdatedAt: now}
	server := httptest.NewServer(gateway)
	defer server.Close()

	response, err := http.Get(server.URL + "/api/jobs/job-1/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}

	reader := bufio.NewReader(response.Body)
	readJob := func() profileJob {
		t.Helper()
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatal(readErr)
		}
		var job profileJob
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(line), "data: ")), &job); err != nil {
			t.Fatal(err)
		}
		_, _ = reader.ReadString('\n')
		return job
	}
	if initial := readJob(); initial.Stage != "queued" {
		t.Fatalf("initial=%#v", initial)
	}

	gateway.updateProfileJob("job-1", "download", "正在下载")
	if update := readJob(); update.Stage != "download" || update.Message != "正在下载" {
		t.Fatalf("update=%#v", update)
	}

	gateway.jobMu.Lock()
	job := gateway.profileJobs["job-1"]
	job.State, job.Stage, job.Message = "done", "done", "完成"
	gateway.publishProfileJobLocked(job)
	gateway.jobMu.Unlock()
	if done := readJob(); done.State != "done" {
		t.Fatalf("done=%#v", done)
	}
}

func TestProfileJobStreamReturnsNotFoundForUnknownJob(t *testing.T) {
	gateway := newGateway(config{})
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/jobs/missing/stream", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
