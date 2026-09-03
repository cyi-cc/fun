package fun

type RequestInfo[T any] struct {
	MethodName  string
	ServiceName string
	Data        *T
	State       map[string]string
}
