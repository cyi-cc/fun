package fun

// 硬化批次回归测试：真实 IP、路由 Guard、started 护栏与优雅停机、
// 内部错误脱敏、流式 writer panic 兜底、匿名 struct 拒绝、TS 大整数往返

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

// ---- 真实 IP：X-Forwarded-For > X-Real-IP > RemoteAddr ----

func TestClientIP(t *testing.T) {
	mk := func(headers map[string]string, remote string) *fasthttp.RequestCtx {
		fc := &fasthttp.RequestCtx{}
		for k, v := range headers {
			fc.Request.Header.Set(k, v)
		}
		if remote != "" {
			fc.SetRemoteAddr(&net.TCPAddr{IP: net.ParseIP(remote), Port: 1234})
		}
		return fc
	}
	if got := clientIP(mk(map[string]string{"X-Forwarded-For": "1.1.1.1, 2.2.2.2"}, "")); got != "2.2.2.2" {
		t.Fatalf("XFF last segment: got %q", got)
	}
	if got := clientIP(mk(map[string]string{"X-Real-IP": "3.3.3.3"}, "")); got != "3.3.3.3" {
		t.Fatalf("X-Real-IP: got %q", got)
	}
	if got := clientIP(mk(nil, "9.9.9.9")); got != "9.9.9.9" {
		t.Fatalf("RemoteAddr fallback: got %q", got)
	}
	if got := clientIP(mk(nil, "")); got != "127.0.0.1" {
		t.Fatalf("no remote addr: got %q", got)
	}
	if got := clientIP(mk(map[string]string{"X-Real-IP": "::1"}, "")); got != "127.0.0.1" {
		t.Fatalf("loopback normalize: got %q", got)
	}
}

type EchoIpSvc struct {
	Ctx
}

func (s *EchoIpSvc) Get() (string, error) { return s.Ip, nil }

func TestClientIPEndtoEnd(t *testing.T) {
	f := New()
	if err := f.BindService(&EchoIpSvc{}); err != nil {
		t.Fatal(err)
	}
	go f.Start(39015)
	time.Sleep(300 * time.Millisecond)
	defer f.Shutdown(context.Background())

	req, err := http.NewRequest("POST", "http://127.0.0.1:39015/cell",
		strings.NewReader(`{"serviceName":"EchoIpSvc","methodName":"Get"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out Result[any]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Status != 0 || out.Data == nil || (*out.Data).(string) != "203.0.113.9" {
		t.Fatalf("client ip not honored: %+v", out)
	}
}

// ---- 路由 Guard：短路 + 查询参数进 State ----

type RouteTokenGuard struct{}

func (g *RouteTokenGuard) Guard(ctx Ctx) error {
	if ctx.State["token"] != "ok" {
		return Error(4401, "unauthorized")
	}
	return nil
}

func TestRouteGuard(t *testing.T) {
	f := New()
	if err := f.BindService(&TestSvc{}); err != nil {
		t.Fatal(err)
	}
	handlerRan := false
	open := func(c *RouteCtx) error {
		handlerRan = true
		c.RequestCtx.WriteString("open")
		return nil
	}
	secret := func(c *RouteCtx) error {
		handlerRan = true
		c.RequestCtx.WriteString("secret")
		return nil
	}
	files := func(c *RouteCtx) error {
		handlerRan = true
		c.RequestCtx.WriteString("file:" + c.Wildcard)
		return nil
	}
	if err := f.BindRoute("GET", "/open", open, &OrderFirstGuard{}); err != nil {
		t.Fatal(err)
	}
	if err := f.BindRoute("GET", "/secret", secret, &OrderRejectGuard{}); err != nil {
		t.Fatal(err)
	}
	if err := f.BindRoute("GET", "/file/*", files, &RouteTokenGuard{}); err != nil {
		t.Fatal(err)
	}

	do := func(path string) *fasthttp.RequestCtx {
		fc := &fasthttp.RequestCtx{}
		fc.Request.Header.SetMethod("GET")
		fc.Request.SetRequestURI(path)
		f.handle(fc)
		return fc
	}

	handlerRan = false
	if fc := do("/open"); !handlerRan || string(fc.Response.Body()) != "open" {
		t.Fatalf("passing guard: ran=%v body=%q", handlerRan, fc.Response.Body())
	}

	handlerRan = false
	fc := do("/secret")
	if handlerRan {
		t.Fatal("handler must not run when guard rejects")
	}
	if body := string(fc.Response.Body()); !strings.Contains(body, `"code":4003`) || !strings.Contains(body, `"status":2`) {
		t.Fatalf("guard rejection body: %s", body)
	}

	// 通配路由 + Guard 从查询参数取 token（State 合并）
	handlerRan = false
	if fc := do("/file/a/b.txt?token=ok"); !handlerRan || string(fc.Response.Body()) != "file:a/b.txt" {
		t.Fatalf("wildcard with token: ran=%v body=%q", handlerRan, fc.Response.Body())
	}
	handlerRan = false
	if fc := do("/file/a.txt"); handlerRan || !strings.Contains(string(fc.Response.Body()), `"code":4401`) {
		t.Fatal("missing token must be rejected by route guard")
	}
}

// ---- started 护栏 + 优雅停机 ----

func TestStartedGuardAndShutdown(t *testing.T) {
	f := New()
	if err := f.BindService(&TestSvc{}); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()

	go f.Start(port)
	time.Sleep(300 * time.Millisecond)
	if err := f.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if err := f.Shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown should be no-op: %v", err)
	}

	catch := func(fn func()) (msg string) {
		defer func() { msg = fmt.Sprint(recover()) }()
		fn()
		return ""
	}
	if m := catch(func() { _ = f.BindService(&TestSvc{}) }); m == "" {
		t.Fatal("BindService after Start must panic")
	}
	if m := catch(func() { _ = f.BindRoute("GET", "/x", func(*RouteCtx) error { return nil }) }); m == "" {
		t.Fatal("BindRoute after Start must panic")
	}
	if m := catch(func() { f.SetTimeouts(time.Second, 0, time.Second) }); m == "" {
		t.Fatal("SetTimeouts after Start must panic")
	}
}

// ---- 内部错误脱敏：客户端只收固定提示，不泄露 Go 内部细节 ----

func TestInternalErrorsSanitized(t *testing.T) {
	f := New()
	if err := f.BindService(&PrecisionSvc{}); err != nil {
		t.Fatal(err)
	}
	go f.Start(39016)
	time.Sleep(300 * time.Millisecond)
	defer f.Shutdown(context.Background())

	post := func(body string) string {
		t.Helper()
		resp, err := http.Post("http://127.0.0.1:39016/cell", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	// data 字段类型错误（字符串进 int64）→ 通用提示，不泄露 unmarshal/类型名
	got := post(`{"serviceName":"PrecisionSvc","methodName":"Get","data":{"id":"not-a-number"}}`)
	if !strings.Contains(got, "invalid request data") {
		t.Fatalf("expected sanitized message, got: %s", got)
	}
	for _, leak := range []string{"unmarshal", "int64", "PrecisionDto", "Go struct"} {
		if strings.Contains(got, leak) {
			t.Fatalf("internal detail %q leaked: %s", leak, got)
		}
	}

	// 损坏的请求体 → 通用提示
	got = post(`{"serviceName":`)
	if !strings.Contains(got, "invalid request body") {
		t.Fatalf("expected sanitized body message, got: %s", got)
	}
}

// ---- 流式 writer panic 兜底：进程不崩、服务器仍可用 ----

type boomMarshaler struct{}

func (boomMarshaler) MarshalJSON() ([]byte, error) { panic("boom-json") }

type PanicStreamSvc struct{}

func (s *PanicStreamSvc) Go() (*Stream, error) {
	st := &Stream{}
	go func() {
		_ = st.Send(boomMarshaler{})
		st.Close()
	}()
	return st, nil
}

func TestStreamWriterPanicRecovered(t *testing.T) {
	f := New()
	if err := f.BindService(&PanicStreamSvc{}); err != nil {
		t.Fatal(err)
	}
	go f.Start(39017)
	time.Sleep(300 * time.Millisecond)
	defer f.Shutdown(context.Background())

	resp, err := http.Post("http://127.0.0.1:39017/cell", "application/json",
		strings.NewReader(`{"serviceName":"PanicStreamSvc","methodName":"Go"}`))
	if err != nil {
		t.Fatalf("first stream request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// panic 被兜底后服务器必须仍然可用
	resp2, err := http.Post("http://127.0.0.1:39017/cell", "application/json",
		strings.NewReader(`{"serviceName":"PanicStreamSvc","methodName":"Go"}`))
	if err != nil {
		t.Fatalf("server died after stream writer panic: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	_ = resp2.Body.Close()
}

// ---- 匿名 struct 注册期拒绝 / isPrivate 空名安全 ----

func TestAnonymousStructRejected(t *testing.T) {
	type withAnon struct {
		Inner struct{ A string }
	}
	defer func() {
		if recover() == nil {
			t.Fatal("anonymous struct must be rejected at registration")
		}
	}()
	checkType(reflect.TypeFor[withAnon]())
	_ = isPrivate("") // 空名不再越界 panic
}

// ---- TS 客户端大整数往返（node 真实执行生成物） ----

func TestTypeScriptBigIntRoundTrip(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "client.ts"), []byte(templateTs{}.genClientTemplate()), 0o644); err != nil {
		t.Fatal(err)
	}
	const script = `
import { Client } from "./client.ts";

// 响应侧：mock 返回原文 JSON（不经 JS number），大整数必须解析为 BigInt
globalThis.fetch = async () => new Response('{"status":0,"data":{"id":9007199254740993}}', {
  headers: { "Content-Type": "application/json" },
});
const client = new Client("http://example.test");
const result = await client.request("Svc", "BigInt");
if (typeof result.data.id !== "bigint") throw new Error("expected bigint, got " + typeof result.data.id);
if (result.data.id !== 9007199254740993n) throw new Error("bigint value lost");

// 请求侧：dto 里的 BigInt 序列化为数字字面量；普通数值不受影响
let requestBody;
globalThis.fetch = async (_url, init) => {
  requestBody = init.body;
  return new Response('{"status":0}', { headers: { "Content-Type": "application/json" } });
};
await client.request("Svc", "BigInt", { id: 9007199254740993n });
if (!requestBody.includes('"id":9007199254740993')) throw new Error("bigint not serialized: " + requestBody);
await client.request("Svc", "Small", { id: 42 });
if (!requestBody.includes('"id":42')) throw new Error("small int broken: " + requestBody);
`
	scriptPath := filepath.Join(dir, "bigint.mjs")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(node, "--experimental-strip-types", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("bigint round trip failed: %v\n%s", err, output)
	}
}
