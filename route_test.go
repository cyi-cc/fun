package fun

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func startRouteServer(t *testing.T, port uint16) *Fun {
	t.Helper()
	f := New()
	f.BindService(&TestSvc{})

	// GET：查询参数 + 纯文本自定义响应
	f.BindRoute("GET", "/ping", func(ctx *RouteCtx) error {
		ctx.RequestCtx.SetContentType("text/plain; charset=utf-8")
		ctx.RequestCtx.WriteString("pong " + ctx.Param("echo"))
		return nil
	})
	// POST：支付回调场景，form-urlencoded 参数合并取值，成功回纯文本 success
	f.BindRoute("POST", "/pay/notify", func(ctx *RouteCtx) error {
		if ctx.Param("trade_status") != "TRADE_SUCCESS" {
			return Error(4001, "invalid trade_status")
		}
		ctx.RequestCtx.SetContentType("text/plain; charset=utf-8")
		ctx.RequestCtx.WriteString("success")
		return nil
	})
	// 返回 error：应输出统一错误响应
	f.BindRoute("GET", "/boom", func(ctx *RouteCtx) error {
		return fmt.Errorf("kaboom")
	})

	go f.Start(port)
	time.Sleep(300 * time.Millisecond)
	return f
}

func TestBindRouteGet(t *testing.T) {
	startRouteServer(t, 39101)
	resp, err := http.Get("http://127.0.0.1:39101/ping?echo=hi")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := make([]byte, 32)
	n, _ := resp.Body.Read(body)
	if got := strings.TrimSpace(string(body[:n])); got != "pong hi" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestBindRoutePayNotifyForm(t *testing.T) {
	startRouteServer(t, 39102)
	form := url.Values{"out_trade_no": {"T123"}, "trade_status": {"TRADE_SUCCESS"}, "money": {"0.01"}}
	resp, err := http.PostForm("http://127.0.0.1:39102/pay/notify", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if got := strings.TrimSpace(string(buf[:n])); got != "success" {
		t.Fatalf("unexpected body: %q", got)
	}

	// 非成功状态 → 统一错误响应
	resp2, err := http.PostForm("http://127.0.0.1:39102/pay/notify",
		url.Values{"trade_status": {"WAIT_BUYER_PAY"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	buf2 := make([]byte, 256)
	n2, _ := resp2.Body.Read(buf2)
	if !strings.Contains(string(buf2[:n2]), "invalid trade_status") {
		t.Fatalf("unexpected error body: %q", string(buf2[:n2]))
	}
}

func TestBindRouteErrorAndCellUnaffected(t *testing.T) {
	startRouteServer(t, 39103)
	// error 路由 → 统一错误 JSON
	resp, err := http.Get("http://127.0.0.1:39103/boom")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "kaboom") {
		t.Fatalf("unexpected error body: %q", string(buf[:n]))
	}
	// 未注册路径仍 404，/cell RPC 不受影响
	if resp2, err := http.Get("http://127.0.0.1:39103/nope"); err != nil || resp2.StatusCode != 404 {
		t.Fatalf("unregistered route should 404: %v %+v", err, resp2)
	} else {
		resp2.Body.Close()
	}
	res := postCell(t, 39103, `{"serviceName":"TestSvc","methodName":"Hello","data":{"name":"tom","age":1}}`)
	if res.Status != 0 || res.Data == nil || (*res.Data).(string) != "hi tom" {
		t.Fatalf("/cell broken by routes: %+v", res)
	}
	// 方法不匹配（GET 打 POST 路由）→ 404
	if resp3, err := http.Get("http://127.0.0.1:39103/pay/notify"); err != nil || resp3.StatusCode != 404 {
		t.Fatalf("method mismatch should 404: %v %+v", err, resp3)
	} else {
		resp3.Body.Close()
	}
}

func TestBindRoutePanics(t *testing.T) {
	f := New()
	catch := func(fn func()) (msg string) {
		defer func() { msg = fmt.Sprint(recover()) }()
		fn()
		return ""
	}
	if m := catch(func() { f.BindRoute("GET", "no-slash", func(*RouteCtx) error { return nil }) }); m == "" {
		t.Fatal("path without leading / should panic")
	}
	if m := catch(func() { f.BindRoute("GET", "/cell", func(*RouteCtx) error { return nil }) }); m == "" {
		t.Fatal("/cell reservation should panic")
	}
	f.BindRoute("get", "/dup", func(*RouteCtx) error { return nil })
	if m := catch(func() { f.BindRoute("GET", "/dup", func(*RouteCtx) error { return nil }) }); m == "" {
		t.Fatal("duplicate route should panic")
	}
}
