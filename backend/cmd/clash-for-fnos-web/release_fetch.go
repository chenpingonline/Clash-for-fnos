package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func fetchRelease(ctx context.Context, repo string) (githubRelease, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	return fetchReleaseWithFallback(ctx, "https://api.github.com/repos/"+repo+"/releases/latest",
		&http.Client{Timeout: 15 * time.Second}, &http.Client{Transport: transport, Timeout: 15 * time.Second})
}

func fetchReleaseWithFallback(ctx context.Context, endpoint string, normal, direct *http.Client) (githubRelease, error) {
	release, firstErr := fetchReleaseOnce(ctx, endpoint, normal)
	if firstErr == nil {
		return release, nil
	}
	if ctx.Err() != nil {
		return githubRelease{}, ctx.Err()
	}
	release, err := fetchReleaseOnce(ctx, endpoint, direct)
	if err != nil {
		return githubRelease{}, fmt.Errorf("检查更新失败：常规请求：%v；不使用 HTTP/HTTPS 代理重试：%v。若已开启 TUN，请检查 Mihomo 分流规则", firstErr, err)
	}
	release.DirectRetry = true
	return release, nil
}
