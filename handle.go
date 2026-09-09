package fun

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/valyala/fasthttp"
)

// handle 处理 HTTP 请求：先匹配自定义路由（BindRoute 精确/通配），未命中走 /cell RPC
func (f *Fun) handle(fastCtx *fasthttp.RequestCtx) {
	ctx := &Ctx{RequestCtx: fastCtx}
	defer f.handlePanic(ctx)

	// CORS 置于路由之前：预检请求在此直接应答，实际请求附加跨域头后继续正常分发
	if f.handleCors(fastCtx) {
		return
	}

	method, path := string(fastCtx.Method()), string(fastCtx.Path())
	if r, ok := f.routes[method+" "+path]; ok {
		f.handleRoute(fastCtx, r, "")
		return
	}
	for _, r := range f.wildcardRoutes[method] {
		if path == r.prefix || strings.HasPrefix(path, r.prefix+"/") {
			f.handleRoute(fastCtx, r.route, strings.TrimPrefix(path, r.prefix+"/"))
			return
		}
	}

	if ctx.path() != "/cell" {
		ctx.setStatusCode(fasthttp.StatusNotFound)
		return
	}
	if !ctx.isPost() {
		ctx.setStatusCode(fasthttp.StatusMethodNotAllowed)
		return
	}

	body := ctx.postBody()
	// Data 以原始字节保存：校验用解码后的 map，业务 DTO 解码用原始字节，
	// 大整数不经过 float64 往返，避免 int64 精度丢失
	var requestInfo RequestInfo[json.RawMessage]
	if err := json.Unmarshal(body, &requestInfo); err != nil {
		ctx.send(internalError("invalid request body", err))
		return
	}
	requestInfo.MethodName = firstLetterToUpper(requestInfo.MethodName)
	requestInfo.ServiceName = firstLetterToUpper(requestInfo.ServiceName)
	if requestInfo.MethodName == "" || requestInfo.ServiceName == "" {
		ctx.sendError(errEmptyFields)
		return
	}

	ctx.Ip = clientIP(fastCtx)
	ctx.State = requestInfo.State
	ctx.MethodName = requestInfo.MethodName
	ctx.ServiceName = requestInfo.ServiceName
	if requestInfo.Data != nil {
		var dataMap map[string]any
		if err := json.Unmarshal(*requestInfo.Data, &dataMap); err != nil {
			ctx.send(internalError("invalid request data", err))
			return
		}
		if dataMap != nil { // "data": null 视为未提供数据
			ctx.Data = &dataMap
			ctx.rawData = *requestInfo.Data
		}
	}

	// 流式方法：响应保持打开，以 NDJSON 行推送（Streamable HTTP）
	// streamCh != nil 表示流式方法；业务返回的 *Stream 在 invoke 内完成注入
	var streamCh chan any
	var streamDone chan struct{}

	result, err := f.invoke(ctx, &streamCh, &streamDone)
	if err != nil {
		ctx.sendError(err)
		return
	}
	if streamCh != nil {
		fastCtx.Response.Header.SetContentType("application/x-ndjson")
		fastCtx.Response.Header.Set("Cache-Control", "no-cache")
		fastCtx.Response.Header.Set("Connection", "keep-alive")
		fastCtx.SetBodyStreamWriter(func(w *bufio.Writer) {
			// 流式写出运行在 fasthttp 的写出 goroutine 上，handle 的 defer 兜不住：
			// 这里必须自行 recover，否则业务 Send 的值 MarshalJSON panic 会击穿进程。
			// finish 保证 streamDone 恰好 close 一次，解除业务 Send 阻塞
			var closeDone sync.Once
			finish := func() { closeDone.Do(func() { close(streamDone) }) }
			defer func() {
				if v := recover(); v != nil {
					ErrorLogger(fmt.Sprintf("fun: stream writer panic (%s.%s): %v", ctx.ServiceName, ctx.MethodName, v), "\n"+string(debug.Stack()))
					finish()
				}
			}()
			writeLine := func(v any) bool {
				data, err := json.Marshal(v)
				if err != nil {
					return false
				}
				raw, err := lowerKeysFromJSON(data)
				if err == nil {
					if data, err = json.Marshal(raw); err != nil {
						return false
					}
				}
				if _, err := fmt.Fprintf(w, "%s\n", data); err != nil {
					return false
				}
				if err := w.Flush(); err != nil {
					return false
				}
				return true
			}
			// (T, stream, error)：T 作为流的第一条消息下发
			if result.Data != nil {
				if !writeLine(*result.Data) {
					finish()
					return
				}
			}
			for message := range streamCh {
				if !writeLine(message) {
					finish()
					return
				}
			}
			finish()
		})
		return
	}
	ctx.send(*result)
}

// handlePanic 兜底处理 panic：完整堆栈记日志；
// 业务以 panic 抛出的 fun.Error 原样透传，其余 panic 只回通用错误——
// panic 消息可能包含 SQL/内部路径等细节，不外泄给客户端
func (f *Fun) handlePanic(c *Ctx) {
	if v := recover(); v != nil {
		var err error
		if e, ok := v.(error); ok {
			err = e
		} else {
			err = fmt.Errorf("panic (%s.%s): %v", c.ServiceName, c.MethodName, v)
		}
		ErrorLogger(err.Error(), "\n"+string(debug.Stack()))
		var result Result[any]
		if errors.As(err, &result) {
			c.sendError(err)
			return
		}
		c.send(internalError("internal error", err))
	}
}

// invoke 按 "Service.Method" 查找并调用，返回成功结果
// 流式方法时，业务返回的 *Stream 完成通道注入（streamCh/streamDone 被创建并填充）
// 预期错误（方法不存在、参数缺失、业务失败）以 error 返回，不 panic
func (f *Fun) invoke(c *Ctx, streamCh *chan any, streamDone *chan struct{}) (*Result[any], error) {
	key := c.ServiceName + "." + c.MethodName
	method, ok := f.methods[key]
	if !ok {
		return nil, errMethodNotFound
	}

	if err := f.callGuard(c, c.ServiceName); err != nil {
		return nil, err
	}

	var args []reflect.Value
	if method.dtoType != nil {
		if c.Data == nil {
			return nil, errDTORequired
		}
		if err := checkDto(method.dtoType, *c.Data, c.MethodName); err != nil {
			return nil, err
		}
		dto := reflect.New(method.dtoType).Elem()
		// 优先按原始字节精确解码（不经 float64）；直接构造 Ctx 调 invoke（无 rawData）时回退 map 往返
		var decodeErr error
		if len(c.rawData) > 0 {
			decodeErr = json.Unmarshal(c.rawData, dto.Addr().Interface())
		} else {
			decodeErr = convert(*c.Data, dto.Addr().Interface())
		}
		if decodeErr != nil {
			// 客户端只收通用提示；详细原因记服务端日志，不外泄 Go 类型等内部信息
			ErrorLogger("fun: decode request data (" + c.ServiceName + "." + c.MethodName + "): ", decodeErr.Error())
			return nil, errInvalidData
		}
		args = append(args, dto)
	}

	// 每请求创建新实例并注入 Ctx/Box 依赖，避免并发共享实例
	instance := reflect.New(method.serviceType)
	f.serviceWired(instance.Elem(), c)
	values := instance.Method(method.methodIndex).Call(args)
	return callResult(c, values, method, streamCh, streamDone)
}

// callResult 将反射调用结果归一为 Result
// 兼容四种签名：(error)、(T, error)、(stream, error)、(T, stream, error)
//   - 末位返回值是 error 且非 nil → 业务失败，返回 error
//   - 带 *Stream 的签名：注入推送通道后，仅 (T, stream, error) 返回 T 作为数据
func callResult(c *Ctx, values []reflect.Value, method methodInfo, streamCh *chan any, streamDone *chan struct{}) (*Result[any], error) {
	if last := values[len(values)-1]; last.Type().Implements(errorType) {
		if !last.IsNil() {
			// 业务出错但可能已启动 goroutine 调 Send/Close：
			// 注入一个已取消的流，让它们立即解除阻塞退出，避免 goroutine 泄漏
			if method.isStream {
				injectCancelledStream(values, method, streamCh, streamDone)
			}
			// 业务 Error() 构造的 Result[any] 作为 error 返回，sendError 里原样透传
			var result Result[any]
			if errors.As(last.Interface().(error), &result) {
				return nil, result
			}
			return nil, last.Interface().(error)
		}
		values = values[:len(values)-1]
	}

	if method.isStream {
		// (stream, error)：流在第 0 位；(T, stream, error)：流在第 1 位
		streamIdx := 0
		if len(values) == 2 {
			streamIdx = 1
		}
		s := values[streamIdx].Interface().(*Stream)
		if s == nil {
			return nil, errors.New("fun: method returned nil stream")
		}
		*streamCh = make(chan any)
		*streamDone = make(chan struct{})
		s.Inject(*streamCh, *streamDone)
		// (T, stream, error)：返回 T；纯流：不返回数据
		if len(values) == 2 {
			r := success(values[0].Interface())
			return &r, nil
		}
		r := success(nil)
		return &r, nil
	}

	if len(values) == 0 {
		// () error：无数据返回
		r := success(nil)
		return &r, nil
	}
	r := success(values[0].Interface())
	return &r, nil
}

// injectCancelledStream 业务出错时注入已取消的流通道，
// 使正在 Send/Close 上阻塞的业务 goroutine 立即解除并退出
func injectCancelledStream(values []reflect.Value, method methodInfo, streamCh *chan any, streamDone *chan struct{}) {
	streamIdx := 0
	if len(values) == 3 { // (T, stream, error)
		streamIdx = 1
	}
	s, ok := values[streamIdx].Interface().(*Stream)
	if !ok || s == nil {
		return
	}
	*streamCh = make(chan any)
	*streamDone = make(chan struct{})
	s.Inject(*streamCh, *streamDone)
	close(*streamDone)
}

// convert 将数据转为 JSON 再反序列化到目标类型，避免手写字段映射
func convert(from any, to any) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, to)
}
