package xerr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error 是业务错误，携带 HTTP 状态码与业务码。
type Error struct {
	Status int
	Code   int
	Msg    string
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func New(status, code int, msg string) *Error {
	return &Error{Status: status, Code: code, Msg: msg}
}

func Wrap(err error, status, code int, msg string) *Error {
	return &Error{Status: status, Code: code, Msg: msg, Err: err}
}

// BadRequest 参数错误。
func BadRequest(msg string) *Error {
	return New(http.StatusBadRequest, 400, msg)
}

// Unauthorized 未登录或 token 失效。
func Unauthorized(msg string) *Error {
	if msg == "" {
		msg = "未登录或登录已过期"
	}
	return New(http.StatusUnauthorized, 401, msg)
}

// Forbidden 无权限。
func Forbidden(msg string) *Error {
	if msg == "" {
		msg = "没有权限访问该资源"
	}
	return New(http.StatusForbidden, 403, msg)
}

// NotFound 资源不存在。
func NotFound(msg string) *Error {
	if msg == "" {
		msg = "资源不存在"
	}
	return New(http.StatusNotFound, 404, msg)
}

// Conflict 业务冲突，如用户名重复。
func Conflict(msg string) *Error {
	return New(http.StatusConflict, 409, msg)
}

// Internal 服务内部错误。
func Internal(err error) *Error {
	return Wrap(err, http.StatusInternalServerError, 500, "服务器内部错误")
}

// Business 业务错误，HTTP 200，前端提示 msg。
func Business(code int, msg string) *Error {
	return New(http.StatusOK, code, msg)
}

// As 提取业务错误。
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
