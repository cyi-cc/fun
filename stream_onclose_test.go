package fun

import (
	"sync/atomic"
	"testing"
	"time"
)

// OnClose 必须允许业务方法在 return *Stream 之前同步注册；旧实现等待 Inject，
// 而 Inject 只有 invoke 在业务方法返回后才调用，形成精确死锁：
//
//	business OnClose -> wait ready -> invoke wait business return -> never Inject.
func TestStreamOnCloseBeforeInjectDoesNotBlock(t *testing.T) {
	st := &Stream[any]{}
	registered := make(chan struct{})
	var called atomic.Int32
	go func() {
		st.OnClose(func() { called.Add(1) })
		close(registered)
	}()
	select {
	case <-registered:
		// expected: registration is independent from channel injection
	case <-time.After(time.Second):
		t.Fatal("OnClose blocked before Inject (business method would deadlock)")
	}

	ch := make(chan any)
	done := make(chan struct{})
	st.Inject(ch, done)
	st.Close()
	if got := called.Load(); got != 1 {
		t.Fatalf("callback called %d times, want 1", got)
	}
}

func TestStreamOnCloseAfterAlreadyClosedRunsImmediately(t *testing.T) {
	st := &Stream[any]{}
	st.Inject(make(chan any), make(chan struct{}))
	st.Close()
	var called atomic.Int32
	st.OnClose(func() { called.Add(1) })
	if got := called.Load(); got != 1 {
		t.Fatalf("callback called %d times, want 1", got)
	}
}
