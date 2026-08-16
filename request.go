package fun

const (
	RequestNormalType uint8 = iota
	RequestStreamType
)

type RequestInfo[T any] struct {
	MethodName  string
	ServiceName string
	Data        *T
	State       map[string]string
	Type        uint8
}
