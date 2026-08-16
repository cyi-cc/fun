package fun

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type TestRepo struct{}

type TestDto struct {
	Name string
	Age  uint8
}

type TestSvc struct {
	Ctx
	Repo *TestRepo
}

var guardHit = false

type TestGuard struct{}

func (g *TestGuard) Guard(ctx Ctx) {
	guardHit = true
}

func (s *TestSvc) Hello(dto TestDto) (string, error) {
	if s.Ip == "" {
		return "", errors.New("no ip")
	}
	if s.Repo == nil {
		return "", errors.New("no repo")
	}
	return "hi " + dto.Name, nil
}

func (s *TestSvc) Count(dto TestDto) (*Stream, error) {
	st := Stream{}
	go func() {
		for i := 0; i < 3; i++ {
			st.Send(fmt.Sprintf("n%d", i))
			time.Sleep(5 * time.Millisecond)
		}
		st.Close()
	}()
	return &st, nil
}

func TestCtxBoxInject(t *testing.T) {
	f := New()
	guardHit = false
	f.BindService(&TestSvc{}, &TestGuard{})
	c := &Ctx{Ip: "1.2.3.4", MethodName: "Hello", ServiceName: "TestSvc"}
	data := map[string]any{"name": "tom", "age": 1}
	c.Data = &data

	var streamCh chan any
	var streamDone chan struct{}
	res, err := f.invoke(c, &streamCh, &streamDone)
	if err != nil {
		t.Fatalf("invoke err: %v", err)
	}
	if (*res.Data).(string) != "hi tom" {
		t.Fatalf("unexpected data: %v", *res.Data)
	}
	if !guardHit {
		t.Fatal("guard not executed")
	}
}

func TestCheckDtoRequired(t *testing.T) {
	f := New()
	f.BindService(&TestSvc{})
	c := &Ctx{Ip: "x", MethodName: "Hello", ServiceName: "TestSvc"}
	data := map[string]any{"age": 1}
	c.Data = &data

	var streamCh chan any
	var streamDone chan struct{}
	_, err := f.invoke(c, &streamCh, &streamDone)
	if err == nil {
		t.Fatal("expected missing-field error")
	}
}

func TestGenCode(t *testing.T) {
	GetFun().BindService(&TestSvc{})
	SetOutput(t.TempDir())
	GenCode(GenGo{}, GenTs{})
	if _, err := os.Stat(filepath.Join(getDirectory(), "go", "test_svc.go")); err != nil {
		t.Fatalf("go service file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(getDirectory(), "ts", "testSvc.ts")); err != nil {
		t.Fatalf("ts service file missing: %v", err)
	}
}

func startServer(t *testing.T, port uint16) *Fun {
	f := New()
	f.BindService(&TestSvc{})
	go f.Start(port)
	time.Sleep(300 * time.Millisecond)
	return f
}

// postCell 以标准库发起 /cell 调用并返回解码后的 Result
func postCell(t *testing.T, port uint16, body string) Result[any] {
	t.Helper()
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/cell", port), "application/json",
		bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out Result[any]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestE2ERequest(t *testing.T) {
	startServer(t, 39001)
	res := postCell(t, 39001, `{"serviceName":"TestSvc","methodName":"Hello","data":{"name":"tom","age":1}}`)
	if res.Status != 0 || res.Data == nil || (*res.Data).(string) != "hi tom" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestE2EStream(t *testing.T) {
	startServer(t, 39002)
	resp, err := http.Post("http://127.0.0.1:39002/cell", "application/json",
		bytes.NewReader([]byte(`{"serviceName":"TestSvc","methodName":"Count","data":{"name":"x","age":1}}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		got = append(got, string(line))
	}
	if len(got) != 3 || got[0] != `"n0"` || got[2] != `"n2"` {
		t.Fatalf("unexpected stream: %v", got)
	}
}
