package fun

import (
	"bytes"
	"encoding/json"
	"net"
	"reflect"

	"github.com/valyala/fasthttp"
)

// Ctx 请求上下文。
// 命名持有 *fasthttp.RequestCtx（非嵌入），避免其方法集提升到服务上，
// 辅助方法全部小写，保证服务方法集只含业务方法。
type Ctx struct {
	Ip          string
	State       map[string]string
	MethodName  string
	ServiceName string
	Data        *map[string]any
	RequestCtx  *fasthttp.RequestCtx

	// rawData 请求 data 字段的原始 JSON 字节（HTTP 路径填充）。
	// 业务 DTO 解码优先用它：不经 map[string]any 的 float64 往返，大整数无精度丢失
	rawData []byte
}

var ctxType = reflect.TypeFor[Ctx]()

func (c *Ctx) path() string                { return string(c.RequestCtx.Path()) }
func (c *Ctx) isPost() bool                { return c.RequestCtx.IsPost() }
func (c *Ctx) postBody() []byte            { return c.RequestCtx.PostBody() }
func (c *Ctx) remoteIP() net.IP            { return c.RequestCtx.RemoteIP() }
func (c *Ctx) setStatusCode(code int)      { c.RequestCtx.SetStatusCode(code) }
func (c *Ctx) write(p []byte) (int, error) { return c.RequestCtx.Write(p) }

// send 写回响应，嵌套对象键统一转小写（兼容 TS/大小写敏感客户端）
func (c *Ctx) send(result Result[any]) {
	data, err := json.Marshal(result)
	if err != nil {
		c.sendError(err)
		return
	}
	raw, err := lowerKeysFromJSON(data)
	if err != nil {
		_, _ = c.write(data)
		return
	}
	out, err := json.Marshal(raw)
	if err != nil {
		_, _ = c.write(data)
		return
	}
	_, _ = c.write(out)
}

// lowerKeysFromJSON 解析 JSON 后递归把所有对象键转为首字母小写。
// 数字以 json.Number 原文保留，不落入 float64——否则响应侧 int64
// 超过 2^53 会丢精度（9007199254740993 → ...992）
func lowerKeysFromJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return lowerKeys(raw), nil
}

func lowerKeys(obj any) any {
	switch v := obj.(type) {
	case map[string]any:
		m := make(map[string]any, len(v))
		for k, val := range v {
			m[firstLetterToLower(k)] = lowerKeys(val)
		}
		return m
	case []any:
		for i := range v {
			v[i] = lowerKeys(v[i])
		}
		return v
	}
	return obj
}

// sendError 写回错误响应
// 业务 Error() 构造的 Result[any] 原样透传保留 Code/Msg/Status，普通 error 包成错误响应
func (c *Ctx) sendError(err error) {
	if result, ok := err.(Result[any]); ok {
		c.send(result)
		return
	}
	c.send(callError(err))
}
