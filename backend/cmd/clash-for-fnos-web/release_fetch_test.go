package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReleaseFallbackBypassesHTTPProxy(t *testing.T) {
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(403) }))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v1.0.5"}`))
	}))
	defer target.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	normalTransport := http.DefaultTransport.(*http.Transport).Clone()
	normalTransport.Proxy = http.ProxyURL(proxyURL)
	defer normalTransport.CloseIdleConnections()
	directTransport := http.DefaultTransport.(*http.Transport).Clone()
	directTransport.Proxy = nil
	defer directTransport.CloseIdleConnections()
	release, err := fetchReleaseWithFallback(context.Background(), target.URL, &http.Client{Transport: normalTransport}, &http.Client{Transport: directTransport})
	if err != nil || !release.DirectRetry || release.TagName != "v1.0.5" || hits.Load() != 1 {
		t.Fatalf("release=%+v err=%v hits=%d", release, err, hits.Load())
	}
}
func TestReleaseSuccessDoesNotRetry(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte(`{"tag_name":"v1.0.5"}`)) }))
	defer target.Close()
	release, err := fetchReleaseWithFallback(context.Background(), target.URL, target.Client(), target.Client())
	if err != nil || release.DirectRetry || hits.Load() != 1 {
		t.Fatalf("%+v %v hits=%d", release, err, hits.Load())
	}
}
func TestReleaseBothFailuresAndCancellation(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(503) }))
	defer target.Close()
	_, err := fetchReleaseWithFallback(context.Background(), target.URL, target.Client(), target.Client())
	if err == nil || !strings.Contains(err.Error(), "重试") || hits.Load() != 2 {
		t.Fatalf("%v hits=%d", err, hits.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fetchReleaseWithFallback(ctx, target.URL, target.Client(), target.Client())
	if err != context.Canceled || hits.Load() != 2 {
		t.Fatalf("%v hits=%d", err, hits.Load())
	}
}
