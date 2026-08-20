package fun

import (
	"fmt"
	"strings"

	"github.com/valyala/fasthttp"
)

// RouteHandler 自定义 HTTP 路由处理器。
//
// 返回 error 时框架统一输出错误响应；返回 nil 视为已自行写回响应——
// 可直接操作 RouteCtx.RequestCtx 完全自定义状态码与内容
// （如支付回调要求的纯文本 "success" 应答）。
type RouteHandler func(ctx *RouteCtx) error

// RouteCtx 自定义路由上下文：Data 合并了 URL 查询参数与 POST 表单参数（表单优先），
// 支付回调等第三方以 form-urlencoded 回调的场景可直接 Param 取值。
// 独立于服务内嵌的 Ctx：后者辅助方法刻意全小写以防混入 RPC 方法集，路由不复用该类型。
type RouteCtx struct {
	RequestCtx *fasthttp.RequestCtx
	Data       map[string]string
}

// Param 取查询/表单参数，不存在返回空串
func (c *RouteCtx) Param(name string) string {
	return c.Data[name]
}

// BindRoute 注册自定义路由（方法大小写不敏感 + 精确路径匹配），用于 GET 直链、
// 健康检查、支付回调等无法走 POST /cell RPC 的场景。
//
//   - path 必须以 "/" 开头；/cell 为 RPC 保留路径，不可注册
//   - 同一 方法+路径 重复注册直接 panic
//   - 与 BindService 一致，需在 Start 前完成注册（启动阶段单线程）
func (f *Fun) BindRoute(method, path string, handler RouteHandler) {
	if handler == nil {
		panic("fun: BindRoute handler cannot be nil")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		panic("fun: BindRoute method cannot be empty")
	}
	if !strings.HasPrefix(path, "/") {
		panic(fmt.Sprintf("fun: BindRoute path %q must start with '/'", path))
	}
	if path == "/cell" {
		panic("fun: /cell is reserved for RPC")
	}
	key := method + " " + path
	if _, exists := f.routes[key]; exists {
		panic(fmt.Sprintf("fun: route %s already bound", key))
	}
	f.routes[key] = handler
}

// handleRoute 执行自定义路由：合并查询与表单参数（application/x-www-form-urlencoded），
// 处理器返回 error 时按统一 Result 格式输出错误响应
func (f *Fun) handleRoute(fastCtx *fasthttp.RequestCtx, handler RouteHandler) {
	data := map[string]string{}
	fastCtx.QueryArgs().VisitAll(func(k, v []byte) {
		data[string(k)] = string(v)
	})
	fastCtx.PostArgs().VisitAll(func(k, v []byte) {
		data[string(k)] = string(v)
	})
	if err := handler(&RouteCtx{RequestCtx: fastCtx, Data: data}); err != nil {
		(&Ctx{RequestCtx: fastCtx}).sendError(err)
	}
}
