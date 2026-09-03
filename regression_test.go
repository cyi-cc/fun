package fun

// 本文件是 v1.3.3 之后五项修复的回归测试：
//  1. 枚举越界值先截断后判范围，256/512 等被洗成合法小值绕过校验
//  2. /cell 请求 data 经 map[string]any 往返，int64 超过 2^53 精度丢失
//  3. Guard 无 error 返回，无法干净短路
//  4. 依赖装配无 error 通道且容器留半初始化实例
//  5. 日志解析失败的文件名直接删除用户文件

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ---- 1. 枚举：越界值必须被拒绝，不能截断洗白 ----

func TestEnumOutOfRangeRejected(t *testing.T) {
	typ := reflect.TypeFor[BugStatus]() // Names: A, B → 合法值 0/1
	mustReject := []any{
		float64(-1), float64(2), float64(3), float64(255),
		float64(256), float64(257), float64(258), float64(1e9), float64(1.5),
		uint16(256), uint32(300), int(-2), int64(999),
	}
	for _, v := range mustReject {
		if err := checkEnumValue(typ, v, "BugStatus"); err == nil {
			t.Fatalf("value %v (%T) should be rejected", v, v)
		}
	}
	mustPass := []any{float64(0), float64(1), uint8(1), int(0), int64(1)}
	for _, v := range mustPass {
		if err := checkEnumValue(typ, v, "BugStatus"); err != nil {
			t.Fatalf("value %v (%T) should pass: %v", v, v, err)
		}
	}
}

// ---- 2. int64 精度：原始字节解码，不经 float64 ----

type PrecisionDto struct {
	Id int64
}

type PrecisionSvc struct{}

func (s *PrecisionSvc) Get(dto PrecisionDto) (int64, error) { return dto.Id, nil }

func TestInt64PrecisionInvoke(t *testing.T) {
	f := New()
	if err := f.BindService(&PrecisionSvc{}); err != nil {
		t.Fatal(err)
	}
	const big = int64(9007199254740993) // 2^53+1，float64 无法精确表示
	c := &Ctx{Ip: "1", MethodName: "Get", ServiceName: "PrecisionSvc"}
	data := map[string]any{"id": float64(big)} // 校验视图只查存在性
	c.Data = &data
	c.rawData = []byte(`{"id":9007199254740993}`) // HTTP 路径的真实输入
	var streamCh chan any
	var streamDone chan struct{}
	res, err := f.invoke(c, &streamCh, &streamDone)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got := (*res.Data).(int64); got != big {
		t.Fatalf("precision lost: got %d want %d", got, big)
	}
}

func TestInt64PrecisionE2E(t *testing.T) {
	f := New()
	if err := f.BindService(&PrecisionSvc{}); err != nil {
		t.Fatal(err)
	}
	go f.Start(39011)
	time.Sleep(300 * time.Millisecond)
	resp, err := http.Post("http://127.0.0.1:39011/cell", "application/json",
		strings.NewReader(`{"serviceName":"PrecisionSvc","methodName":"Get","data":{"id":9007199254740993}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body := make([]byte, 512)
	n, _ := resp.Body.Read(body)
	if !strings.Contains(string(body[:n]), "9007199254740993") {
		t.Fatalf("int64 precision lost in response: %s", string(body[:n]))
	}
}

// ---- 3. Guard：返回 error 短路，业务方法不得执行 ----

var guardOrderLog []string

type OrderFirstGuard struct{}

func (g *OrderFirstGuard) Guard(ctx Ctx) error {
	guardOrderLog = append(guardOrderLog, "first")
	return nil
}

type OrderRejectGuard struct{}

func (g *OrderRejectGuard) Guard(ctx Ctx) error {
	guardOrderLog = append(guardOrderLog, "reject")
	return Error(4003, "denied")
}

type OrderLastGuard struct{}

func (g *OrderLastGuard) Guard(ctx Ctx) error {
	guardOrderLog = append(guardOrderLog, "last") // 短路后不应执行
	return nil
}

var guardedMethodRan bool

type GuardedSvc struct {
	Ctx
}

func (s *GuardedSvc) Ping() error {
	guardedMethodRan = true
	return nil
}

func TestGuardShortCircuit(t *testing.T) {
	f := New()
	if err := f.BindService(&GuardedSvc{}, &OrderFirstGuard{}, &OrderRejectGuard{}, &OrderLastGuard{}); err != nil {
		t.Fatal(err)
	}
	guardOrderLog = nil
	guardedMethodRan = false
	c := &Ctx{Ip: "1", MethodName: "Ping", ServiceName: "GuardedSvc"}
	var streamCh chan any
	var streamDone chan struct{}
	_, err := f.invoke(c, &streamCh, &streamDone)
	if err == nil {
		t.Fatal("expected guard rejection")
	}
	if guardedMethodRan {
		t.Fatal("business method must not run after guard rejection")
	}
	if len(guardOrderLog) != 2 || guardOrderLog[0] != "first" || guardOrderLog[1] != "reject" {
		t.Fatalf("guards after rejection must not run: %v", guardOrderLog)
	}
	var result Result[any]
	if !errors.As(err, &result) {
		t.Fatalf("error should carry Result, got %T", err)
	}
	if result.Code == nil || *result.Code != 4003 || result.Status != errorCode {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// ---- 4. 依赖装配：error 通道 + 粘性失败 ----

var wOkBoxNewCalled bool

type WOkBox struct{}

func (b *WOkBox) New() error { wOkBoxNewCalled = true; return nil }

type WFailBox struct{}

func (b *WFailBox) New() error { return errors.New("connect refused") }

type WParentBox struct {
	Fail *WFailBox `fun:"auto"`
}

type WSvc struct {
	Ctx
	Fail *WFailBox
}

func (s *WSvc) Ping() error { return nil }

func TestWiredErrorAndStickyFailure(t *testing.T) {
	old := fun
	fun = New()
	defer func() { fun = old }()

	wOkBoxNewCalled = false
	if _, err := Wired[WOkBox](); err != nil {
		t.Fatalf("ok box: %v", err)
	}
	if !wOkBoxNewCalled {
		t.Fatal("New() not called")
	}

	_, err := Wired[WFailBox]()
	if err == nil || !strings.Contains(err.Error(), "connect refused") {
		t.Fatalf("expected failure, got %v", err)
	}
	// 粘性错误：重复 Wired 返回同一错误，不重试
	if _, err2 := Wired[WFailBox](); err2 == nil || err2.Error() != err.Error() {
		t.Fatalf("sticky failure expected, got %v then %v", err, err2)
	}
	// 失败类型不暴露半初始化实例
	if entry, ok := fun.boxes.Load(reflect.TypeFor[*WFailBox]()); ok {
		if e := entry.(boxEntry); e.err == nil {
			t.Fatal("failed box must not hold a usable instance")
		}
	}
}

func TestWiredNestedFailurePropagates(t *testing.T) {
	old := fun
	fun = New()
	defer func() { fun = old }()

	if _, err := Wired[WParentBox](); err == nil || !strings.Contains(err.Error(), "connect refused") {
		t.Fatalf("nested failure should propagate, got %v", err)
	}
}

func TestBindServiceWireFailurePropagates(t *testing.T) {
	f := New()
	if err := f.BindService(&WSvc{}); err == nil {
		t.Fatal("BindService should propagate wiring failure")
	}
}

// ---- 5. 日志：解析失败的文件名不删除 ----

func TestLoggerKeepsUnknownFiles(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, []byte("keep me"), 0644); err != nil {
		t.Fatal(err)
	}
	ConfigLogger(Logger{Level: TraceLevel, Mode: FileMode, LogFilePath: dir, ExpireLogsDays: 3})
	defer ConfigLogger(Logger{Level: TraceLevel, Mode: TerminalMode})

	// 写入路径：getNextLogFile 遍历目录解析文件名，旧逻辑会删除解析失败的文件
	fileLogger("[test] hello")
	// 清理路径
	cleanupExpiredLogs()

	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("unknown file deleted: %v", err)
	}
}
