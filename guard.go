package fun

// Guard 方法调用前的拦截器。
//
// 返回 nil 放行；返回 error 短路：后续 Guard 与业务方法不再执行，
// error 走统一 Result 错误响应（返回 fun.Error(code, msg) 可携带错误码）。
// 不再需要通过写响应或 panic 表达拒绝
type Guard interface {
	Guard(ctx Ctx) error
}
