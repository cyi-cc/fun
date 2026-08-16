package fun

type templateGo struct{}

func (ctx templateGo) genDefaultServiceTemplate() string {
	return `package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Result 统一响应结构
type Result[T any] struct {
	Id     string
	Code   *uint16
	Data   *T
	Msg    *string
	Status uint8
}

func (r Result[T]) Error() string {
	if r.Msg != nil {
		return *r.Msg
	}
	if r.Code != nil {
		return fmt.Sprintf("code=%d", *r.Code)
	}
	return "api: unknown error"
}

// Void 用于无数据返回的方法
type Void = struct{}

// RequestInterceptor 请求前拦截器：可鉴权、加签、改 dto；返回 error 则直接失败
type RequestInterceptor func(serviceName string, methodName string, dto any) error

// ResponseInterceptor 响应后拦截器：可记录日志、埋点、解密；返回 error 则转为失败响应
type ResponseInterceptor func(serviceName string, methodName string, result Result[any]) error

// Client 内联 HTTP 客户端，不依赖外部 funclient 包
type Client struct {
	url                  string
	client               *http.Client
	state                map[string]string
	requestInterceptors  []RequestInterceptor
	responseInterceptors []ResponseInterceptor
}

// NewClient 创建客户端
func NewClient(url string) (*Client, error) {
	return &Client{
		url:    strings.TrimRight(url, "/"),
		client: &http.Client{},
	}, nil
}

// SetHttpClient 替换底层 http.Client
func (c *Client) SetHttpClient(client *http.Client) {
	c.client = client
}

// AddRequestInterceptor 注册请求前拦截器
func (c *Client) AddRequestInterceptor(i RequestInterceptor) {
	c.requestInterceptors = append(c.requestInterceptors, i)
}

// AddResponseInterceptor 注册响应后拦截器
func (c *Client) AddResponseInterceptor(i ResponseInterceptor) {
	c.responseInterceptors = append(c.responseInterceptors, i)
}

// SetState 设置随每个请求携带的状态（如 token），服务端 Guard 可读取
func (c *Client) SetState(state map[string]string) {
	c.state = state
}

// Request 发起普通调用
func Request[T any](c *Client, serviceName string, methodName string, dto ...any) Result[T] {
	payload := newPayload(serviceName, methodName, dto, c.state)
	b, err := json.Marshal(payload)
	if err != nil {
		return Result[T]{Status: 2, Msg: ptr(err.Error())}
	}
	for _, i := range c.requestInterceptors {
		var dtoVal any
		if len(dto) > 0 {
			dtoVal = dto[0]
		}
		if err := i(serviceName, methodName, dtoVal); err != nil {
			return Result[T]{Status: 2, Msg: ptr(err.Error())}
		}
	}
	req, err := http.NewRequest(http.MethodPost, c.url+"/cell", bytes.NewReader(b))
	if err != nil {
		return Result[T]{Status: 2, Msg: ptr(err.Error())}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return Result[T]{Status: 2, Msg: ptr(err.Error())}
	}
	defer resp.Body.Close()
	var out Result[T]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Result[T]{Status: 2, Msg: ptr(err.Error())}
	}
	var anyData *any
	if out.Data != nil {
		v := any(*out.Data)
		anyData = &v
	}
	anyResult := Result[any]{Id: out.Id, Code: out.Code, Data: anyData, Msg: out.Msg, Status: out.Status}
	for _, i := range c.responseInterceptors {
		if err := i(serviceName, methodName, anyResult); err != nil {
			return Result[T]{Status: 2, Msg: ptr(err.Error())}
		}
	}
	return out
}

// Stream 发起流式调用，通过 NDJSON 行逐个推送消息（Streamable HTTP）
func Stream[T any](c *Client, serviceName string, methodName string, dto ...any) (<-chan T, error) {
	payload := newPayload(serviceName, methodName, dto, c.state)
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	for _, i := range c.requestInterceptors {
		var dtoVal any
		if len(dto) > 0 {
			dtoVal = dto[0]
		}
		if err := i(serviceName, methodName, dtoVal); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequest(http.MethodPost, c.url+"/cell", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("api: unexpected status %d", resp.StatusCode)
	}
	anyResult := Result[any]{Status: 0}
	for _, i := range c.responseInterceptors {
		if err := i(serviceName, methodName, anyResult); err != nil {
			resp.Body.Close()
			return nil, err
		}
	}
	ch := make(chan T)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var msg T
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				continue
			}
			ch <- msg
		}
	}()
	return ch, nil
}

func newPayload(serviceName string, methodName string, dto []any, state map[string]string) map[string]any {
	payload := map[string]any{
		"serviceName": serviceName,
		"methodName":  methodName,
	}
	if len(dto) > 0 {
		payload["data"] = dto[0]
	}
	if len(state) > 0 {
		payload["state"] = state
	}
	return payload
}

func ptr(s string) *string { return &s }

type Api struct {
{{- range .GenServiceList}}
	{{.ServiceName}} *{{.ServiceName}}
{{- end}}
	*Client
}

func CreateApi(url string) (Api, error) {
	apiClient, err := NewClient(url)
	return Api{
{{- range .GenServiceList}}
		{{.ServiceName}}: New{{.ServiceName}}(apiClient),
{{- end}}
		Client: apiClient,
	}, err
}`
}

func (ctx templateGo) genServiceTemplate() string {
	return `package api

type {{.ServiceName}} struct {
	*Client
}

func New{{.ServiceName}}(client *Client) *{{.ServiceName}} {
	return &{{.ServiceName}}{
		Client: client,
	}
}

{{- $serviceName := .ServiceName }}
{{- range .GenMethodTypeList}}
{{if .IsStream }}func (ctx *{{$serviceName}}) {{.MethodName}}({{.DtoText}}) (<-chan {{.GenericTypeText}}, error) {
	return Stream[{{.GenericTypeText}}](ctx.Client, "{{$serviceName}}", "{{.MethodName}}"{{.ArgsText}})
}{{else}}func (ctx *{{$serviceName}}) {{.MethodName}}({{.DtoText}}) {{.ReturnValueText}} {
	return Request[{{.GenericTypeText}}](ctx.Client, "{{$serviceName}}", "{{.MethodName}}"{{.ArgsText}})
}{{end}}
{{- end}}`
}

func (ctx templateGo) genStructTemplate() string {
	return `package api

type {{.Name}} struct{
  {{- range .GenClassFieldType}}
    {{.Name}} {{.Type}} {{.Tag}}
  {{- end}}
}`
}

func (ctx templateGo) genEnumTemplate() string {
	return `package api

type {{.Name}} uint8

{{$enumName := .Name}}
const (
{{- range $index, $element := .Names}}
    {{$element}}{{if eq $index 0}}        {{$enumName}} = iota{{end}}
{{- end}}
)

func ({{.Name}}) Values() []{{.Name}} {
	return []{{.Name}}{
{{- range $index, $element := .Names}}
        {{$element}},
{{- end}}
	}
}

{{if .DisplayNames}}
func ({{.Name}}) DisplayNames() []string {
	return []string{
{{- range $index, $element := .DisplayNames}}
        "{{$element}}",
{{- end}}
	}
}
{{end}}`
}
