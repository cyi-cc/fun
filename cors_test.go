package fun

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

func startCorsServer(t *testing.T, port uint16, withCors bool) *Fun {
	t.Helper()
	f := New()
	if err := f.BindService(&TestSvc{}); err != nil {
		t.Fatal(err)
	}
	if withCors {
		f.CORS("https://a.com", "HTTPS://B.Com") // 大小写归一化
	}
	f.BindRoute("GET", "/ping", func(ctx *RouteCtx) error {
		ctx.RequestCtx.WriteString("pong")
		return nil
	})
	go f.Start(port)
	time.Sleep(300 * time.Millisecond)
	return f
}

func corsGet(t *testing.T, url, origin string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestCorsWhitelistEcho(t *testing.T) {
	startCorsServer(t, 39201, true)
	resp := corsGet(t, "http://127.0.0.1:39201/ping", "https://a.com")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://a.com" {
		t.Fatalf("allow-origin = %q, want echo", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("allow-credentials = %q", got)
	}
	if got := resp.Header.Get("Vary"); !strings.Contains(got, "Origin") {
		t.Fatalf("vary = %q, want Origin", got)
	}

	// 白名单大小写不敏感
	resp2 := corsGet(t, "http://127.0.0.1:39201/ping", "https://B.COM")
	defer resp2.Body.Close()
	if got := resp2.Header.Get("Access-Control-Allow-Origin"); got != "https://B.COM" {
		t.Fatalf("allow-origin = %q, want case-insensitive match", got)
	}
}

func TestCorsOriginNotWhitelisted(t *testing.T) {
	startCorsServer(t, 39202, true)
	resp := corsGet(t, "http://127.0.0.1:39202/ping", "https://evil.com")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d（未命中白名单请求本身仍应正常处理）", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin = %q, want empty", got)
	}
}

func TestCorsPreflight(t *testing.T) {
	startCorsServer(t, 39203, true)
	req, _ := http.NewRequest("OPTIONS", "http://127.0.0.1:39203/cell", nil)
	req.Header.Set("Origin", "https://a.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type, x-custom")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://a.com" {
		t.Fatalf("allow-origin = %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Fatalf("allow-methods = %q, want POST", got)
	}
	// 请求头回显
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "x-custom") {
		t.Fatalf("allow-headers = %q, want echo x-custom", got)
	}
	if got := resp.Header.Get("Access-Control-Max-Age"); got != "86400" {
		t.Fatalf("max-age = %q", got)
	}
}

func TestCorsHeadersOnCellRpc(t *testing.T) {
	startCorsServer(t, 39204, true)
	body := `{"ServiceName":"TestSvc","MethodName":"Hello","Data":{"Name":"tom","Age":1}}`
	req, _ := http.NewRequest("POST", "http://127.0.0.1:39204/cell", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://a.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://a.com" {
		t.Fatalf("/cell 响应缺少 CORS 头: %q", got)
	}
}

func TestCorsNotConfigured(t *testing.T) {
	startCorsServer(t, 39205, false)
	resp := corsGet(t, "http://127.0.0.1:39205/ping", "https://a.com")
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("未配置 CORS 却有 allow-origin = %q", got)
	}
}
