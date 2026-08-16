package fun

import (
	"fmt"
	"reflect"
)

const (
	successCode uint8 = iota
	cellErrorCode
	errorCode
)

type Result[T any] struct {
	Id     string  `json:"id,omitempty"`
	Code   *uint16 `json:"code,omitempty"`
	Data   *T      `json:"data,omitempty"`
	Msg    *string `json:"msg,omitempty"`
	Status uint8   `json:"status"`
}

// Error 让 Result 实现 error 接口，业务方法可直接返回，Code/Msg/Status 随结果透传
func (r Result[T]) Error() string {
	if r.Msg != nil {
		return *r.Msg
	}
	if r.Code != nil {
		return fmt.Sprintf("code=%d", *r.Code)
	}
	return "fun: unknown error"
}

// Error 构造带错误码的错误响应，作为 error 返回
// 用法：return "", fun.Error(4001, "登录失败")
func Error(code uint16, msg string) error {
	return Result[any]{Code: &code, Msg: &msg, Status: errorCode}
}

func callError(err error) Result[any] {
	return Result[any]{Msg: new(err.Error()), Status: cellErrorCode}
}

// success 构造成功响应,空切片规范化为 [] 而不是 null
func success(data any) Result[any] {
	return Result[any]{Data: nonNil(data), Status: successCode}
}

// nonNil 返回 data 的指针;空切片会重建为同类型的非 nil 空切片,
// 保证 JSON 序列化输出 [] 而不是 null
func nonNil(data any) *any {
	if data == nil {
		return nil
	}

	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Slice && v.Len() == 0 {
		return new(reflect.MakeSlice(v.Type(), 0, 0).Interface())
	}
	return &data
}
