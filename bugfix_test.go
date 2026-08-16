package fun

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type BugStatus uint8

func (BugStatus) Names() []string { return []string{"A", "B"} }

type BugDto struct {
	Status *BugStatus
}

type BugSvc struct{}

func (s *BugSvc) Ping() error { return nil }

func (s *BugSvc) Save(dto BugDto) (string, error) { return "ok", nil }

func (s *BugSvc) Ticker() (string, *Stream, error) {
	st := Stream{}
	go func() {
		st.Send("tick")
		st.Close()
	}()
	return "first", &st, nil
}

func bugInvoke(t *testing.T, method string, data map[string]any) (*Result[any], error) {
	t.Helper()
	f := New()
	f.BindService(&BugSvc{})
	if data == nil {
		data = map[string]any{}
	}
	c := &Ctx{Ip: "1", MethodName: method, ServiceName: "BugSvc", Data: &data}
	var streamCh chan any
	var streamDone chan struct{}
	return f.invoke(c, &streamCh, &streamDone)
}

// bug1: () error 签名的方法 invoke 应返回空数据结果而不是越界 panic
func TestBugErrorOnlyInvoke(t *testing.T) {
	res, err := bugInvoke(t, "Ping", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Data != nil {
		t.Fatalf("expect nil data, got %v", *res.Data)
	}
}

// bug2: 指针枚举字段传 null 应放行；越界值仍要报错
func TestBugNullableEnum(t *testing.T) {
	if _, err := bugInvoke(t, "Save", map[string]any{"status": nil}); err != nil {
		t.Fatalf("nullable enum should pass: %v", err)
	}
	if _, err := bugInvoke(t, "Save", map[string]any{"status": 1}); err != nil {
		t.Fatalf("valid enum should pass: %v", err)
	}
	if _, err := bugInvoke(t, "Save", map[string]any{"status": 5}); err == nil {
		t.Fatal("out-of-range enum should fail")
	}
}

// bug3: 含 () error 方法的代码生成不应 panic，且类型应生成为 Void/void
func TestBugGenErrorOnly(t *testing.T) {
	GetFun().BindService(&BugSvc{})
	SetOutput(t.TempDir())
	GenCode(GenGo{}, GenTs{})
	goSrc, err := os.ReadFile(filepath.Join(getDirectory(), "go", "bug_svc.go"))
	if err != nil {
		t.Fatalf("go file missing: %v", err)
	}
	if !strings.Contains(string(goSrc), "Result[Void]") {
		t.Fatalf("go: expect Result[Void], got:\n%s", goSrc)
	}
	tsSrc, err := os.ReadFile(filepath.Join(getDirectory(), "ts", "bugSvc.ts"))
	if err != nil {
		t.Fatalf("ts file missing: %v", err)
	}
	if !strings.Contains(string(tsSrc), "result<void>") {
		t.Fatalf("ts: expect result<void>, got:\n%s", tsSrc)
	}
}

// bug4+5: 响应键应为小写；(T, stream, error) 的 T 应作为流的第一条消息下发
func TestBugJsonKeysAndStreamFirst(t *testing.T) {
	f := New()
	f.BindService(&BugSvc{})
	go f.Start(39003)
	time.Sleep(300 * time.Millisecond)

	resp, err := http.Post("http://127.0.0.1:39003/cell", "application/json",
		strings.NewReader(`{"serviceName":"BugSvc","methodName":"Ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	_ = resp.Body.Close()
	body := buf.String()
	if !strings.Contains(body, `"status"`) || strings.Contains(body, `"Status"`) {
		t.Fatalf("keys not lowercase: %s", body)
	}

	resp2, err := http.Post("http://127.0.0.1:39003/cell", "application/json",
		bytes.NewReader([]byte(`{"serviceName":"BugSvc","methodName":"Ticker"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var got []string
	scanner := bufio.NewScanner(resp2.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg string
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("bad ndjson line %q: %v", line, err)
		}
		got = append(got, msg)
	}
	if len(got) != 2 || got[0] != "first" || got[1] != "tick" {
		t.Fatalf("stream: %v", got)
	}
}

// bug7: 非指针切片字段传 null 应放行（JSON null -> nil slice）
type NullSlicDto struct {
	Tags []string
}

type NullSlicSvc struct{}

func (s *NullSlicSvc) Save(dto NullSlicDto) (string, error) { return "ok", nil }

func TestBugSliceNull(t *testing.T) {
	f := New()
	f.BindService(&NullSlicSvc{})
	data := map[string]any{"tags": nil}
	c := &Ctx{Ip: "1", MethodName: "Save", ServiceName: "NullSlicSvc", Data: &data}
	var streamCh chan any
	var streamDone chan struct{}
	if _, err := f.invoke(c, &streamCh, &streamDone); err != nil {
		t.Fatalf("slice null should pass: %v", err)
	}
}

// bug8: 业务方法返回 error 但已启动 goroutine 调 Send，框架应注入取消流让 goroutine 退出而非挂死
var leakDone chan struct{}

type LeakSvc struct{}

func (s *LeakSvc) Fail() (*Stream, error) {
	st := Stream{}
	leakDone = make(chan struct{})
	go func() {
		st.Send("never")
		close(leakDone)
	}()
	return &st, errors.New("boom")
}

func TestBugStreamLeak(t *testing.T) {
	f := New()
	f.BindService(&LeakSvc{})
	c := &Ctx{Ip: "1", MethodName: "Fail", ServiceName: "LeakSvc"}
	var streamCh chan any
	var streamDone chan struct{}
	_, err := f.invoke(c, &streamCh, &streamDone)
	if err == nil {
		t.Fatal("expected error")
	}
	// Send goroutine 必须解除阻塞（有超时保护，防挂死）
	select {
	case <-leakDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Send goroutine blocked forever: stream leak")
	}
}

// bug6: 与 fasthttp.RequestCtx 方法重名的用户方法不应被静默丢弃
type CollideSvc struct {
	Ctx
}

func (s *CollideSvc) Cookie() (string, error) { return "cookie", nil }

func TestBugMethodNameCollision(t *testing.T) {
	f := New()
	f.BindService(&CollideSvc{})
	if _, ok := f.methods["CollideSvc.Cookie"]; !ok {
		t.Fatal("Cookie method dropped due to name collision with fasthttp.RequestCtx")
	}
	res, err := func() (*Result[any], error) {
		c := &Ctx{Ip: "1", MethodName: "Cookie", ServiceName: "CollideSvc"}
		data := map[string]any{}
		c.Data = &data
		var streamCh chan any
		var streamDone chan struct{}
		return f.invoke(c, &streamCh, &streamDone)
	}()
	if err != nil {
		t.Fatalf("invoke Cookie err: %v", err)
	}
	if (*res.Data).(string) != "cookie" {
		t.Fatalf("unexpected: %v", *res.Data)
	}
}
