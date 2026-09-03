package fun

import (
	"errors"
	"reflect"
)

type enum interface {
	Names() []string
}

type displayEnum interface {
	DisplayNames() []string
	Names() []string
}

var (
	enumType        = reflect.TypeFor[enum]()
	displayEnumType = reflect.TypeFor[displayEnum]()
)

var (
	errMethodNotFound = errors.New("method not found")
	errEmptyFields    = errors.New("serviceName and methodName cannot be empty")
	errDTORequired    = errors.New("method requires a DTO but none provided")
	errInvalidData    = errors.New("invalid request data") // 详细原因只记服务端日志
)
