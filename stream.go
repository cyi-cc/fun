package fun

import (
	"fmt"
	"sync"
)

// Stream 流式响应的业务句柄。
// 业务方法返回 *Stream 后，框架调用 Inject 注入推送通道；
// 未注入时 Send/Close 自动阻塞等待，避免业务 goroutine 与注入之间的竞态。
type Stream struct {
	mu      sync.Mutex
	once    sync.Once
	ready   chan struct{}
	ch      chan any
	done    chan struct{}
	closed  bool
	onClose func()
}

func (s *Stream) getReady() chan struct{} {
	s.once.Do(func() {
		if s.ready == nil {
			s.ready = make(chan struct{})
		}
	})
	return s.ready
}

// Inject 注入推送通道与结束信号，由框架在方法返回后调用
func (s *Stream) Inject(ch chan any, done chan struct{}) {
	s.ch = ch
	s.done = done
	close(s.getReady())
}

// Send 推送一条消息；连接断开或流已关闭时返回错误
func (s *Stream) Send(message any) error {
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
func (s *Stream) Close() {
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

// OnClose 注册关闭回调；可在业务方法 return *Stream 之前同步调用。
// 不得等待 Inject：Inject 只有业务方法返回后才发生，等待会形成循环依赖死锁。
// 流已关闭时在锁外立即执行回调，避免回调重入 Stream 时自锁。
func (s *Stream) OnClose(cb func()) {
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
