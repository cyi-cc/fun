// Package demo 演示 fun 框架各特性的示例服务集：
// 定宽整型与可空指针字段、枚举、业务错误码、NDJSON 流式（纯流 + 首条消息流）。
// 产物参考见 example/gen（由 cmd/genexample 生成）。
package demo

import (
	"github.com/cyi-cc/fun"
)

// OrderStatus 订单状态枚举：uint8 底层 + Names/DisplayNames
type OrderStatus uint8

func (OrderStatus) Names() []string        { return []string{"Pending", "Paid", "Shipped"} }
func (OrderStatus) DisplayNames() []string { return []string{"待支付", "已支付", "已发货"} }

// CreateOrderDto 下单参数。规则：非指针字段必传；指针/slice 字段可省略；
// 数值一律定宽整型（int64），小数用 string 传输
type CreateOrderDto struct {
	Sku    string    // 必传
	Count  int64     // 必传
	Note   *string   // 可空
	Status *OrderStatus // 可空，缺省 Pending
	Tags   []string // 可省略
}

// OrderDto 订单视图。Id 为雪花 ID：全链路 int64 精度，TS 侧解析为 BigInt
type OrderDto struct {
	Id      int64
	Status  OrderStatus
	Amount  string   // 小数金额用字符串传输
	Items   []string
}

type GetOrderDto struct {
	Id int64
}

type CancelDto struct {
	Id     int64
	Reason *string
}

// OrderSvc 订单服务：普通请求/响应、无 DTO、error-only、业务错误码
type OrderSvc struct {
	fun.Ctx
}

func (s *OrderSvc) Create(dto CreateOrderDto) (OrderDto, error) {
	return OrderDto{
		Id:     9007199254740993, // 超过 2^53 的大整数示例
		Status: OrderStatus(0),
		Amount: "199.00",
		Items:  dto.Tags,
	}, nil
}

func (s *OrderSvc) Get(dto GetOrderDto) (OrderDto, error) {
	return OrderDto{Id: dto.Id, Status: OrderStatus(1), Amount: "0.01"}, nil
}

// Cancel error-only 签名：无返回数据
func (s *OrderSvc) Cancel(dto CancelDto) error {
	if dto.Reason == nil {
		// 业务错误：Code/Msg 原样透传给前端（status=2）
		return fun.Error(4004, "必须填写取消原因")
	}
	return nil
}

type ChatDto struct {
	Prompt string
}

type AskDto struct {
	Prompt string
}

// ChatSvc 流式服务：纯流 + 首条消息流两种签名
type ChatSvc struct {
	fun.Ctx
}

// Chat 纯流式：响应为 application/x-ndjson，逐行推送
func (s *ChatSvc) Chat(dto ChatDto) (*fun.Stream, error) {
	st := &fun.Stream{}
	go func() {
		for _, chunk := range []string{"你好", "，这是", "流式示例"} {
			if err := st.Send(chunk); err != nil {
				return // 连接断开
			}
		}
		st.Close() // 必须关闭，否则客户端一直等
	}()
	return st, nil
}

// Ask (T, stream, error)：T 作为流的第一条消息下发
func (s *ChatSvc) Ask(dto AskDto) (string, *fun.Stream, error) {
	st := &fun.Stream{}
	go func() {
		_ = st.Send("思考中...")
		st.Close()
	}()
	return "收到：" + dto.Prompt, st, nil
}
