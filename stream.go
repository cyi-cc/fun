package fun

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// streamMsgType 提取 *Stream[T] 的消息类型 T；非 Stream 指针返回 nil。
// reflect 没有类型实参 API，但实例化类型的 Send 方法签名已替换——In(1) 即 T
func streamMsgType(t reflect.Type) reflect.Type {
	if t.Kind() != reflect.Ptr || !strings.HasPrefix(t.Elem().Name(), "Stream[") {
		return nil
	}
	sm, ok := t.MethodByName("Send")
	if !ok || sm.Type.NumIn() != 2 {
		return nil
	}
	if _, ok := t.MethodByName("Inject"); !ok {
		return nil
	}
	return sm.Type.In(1)
}

// Stream 流式响应的业务句柄。
// 业务方法返回 *Stream[T] 后，框架调用 Inject 注入推送通道；
// 未注入时 Send/Close 自动阻塞等待，避免业务 goroutine 与注入之间的竞态。
type Stream[T any] struct {
	mu      sync.Mutex
	once    sync.Once
	ready   chan struct{}
	ch      chan any
	done    chan struct{}
	closed  bool
	onClose func()
}

func (s *Stream[T]) getReady() chan struct{} {
	s.once.Do(func() {
		if s.ready == nil {
			s.ready = make(chan struct{})
		}
	})
	return s.ready
}

// Inject 注入推送通道与结束信号，由框架在方法返回后调用
func (s *Stream[T]) Inject(ch chan any, done chan struct{}) {
	s.ch = ch
	s.done = done
	close(s.getReady())
}

// Send 推送一条消息；连接断开或流已关闭时返回错误
func (s *Stream[T]) Send(message T) error {
	<-s.getReady()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("fun: stream closed")
	}
	select {
	case s.ch <- message:
		return nil
	case <-s.done:
		return fmt.Errorf("fun: stream closed")
	}
}

// Close 主动结束流，触发 OnClose 回调
func (s *Stream[T]) Close() {
	<-s.getReady()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	cb := s.onClose
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
	close(s.ch)
}

// OnClose 注册关闭回调；可在业务方法 return *Stream[T] 之前同步调用。
// 不得等待 Inject：Inject 只有业务方法返回后才发生，等待会形成循环依赖死锁。
// 流已关闭时在锁外立即执行回调，避免回调重入 Stream 时自锁。
func (s *Stream[T]) OnClose(cb func()) {
	if cb == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cb()
		return
	}
	s.onClose = cb
	s.mu.Unlock()
}
