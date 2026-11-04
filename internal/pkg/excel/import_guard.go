package excel

import (
	"errors"
	"fmt"
	"io"
	"time"
)

// guard 固定的默认并发数与收紧后的解压上限。
const (
	// GuardDefaultConcurrent 是导入 guard 的默认最大并发数。
	GuardDefaultConcurrent = 2
	// GuardMaxUnzipBytes 是导入 guard 固定的解压后总大小上限（80MB），
	// 比 helper 默认的 MaxBytes*20（200MB）更收紧。
	GuardMaxUnzipBytes int64 = 80 << 20
)

// ImportGuard 是应用级导入 guard：固定 helper 的导入限额、用信号量限制
// 同时解析的导入数，并把 helper 错误归类为类型化业务错误（见 GuardErrorKind）。
//
// guard 实例是应用级组件，应在启动时创建一次、多个请求共享：
//
//	g := excel.NewImportGuard[CustomerRow](2)
//	items, err := g.Import(r, excel.GuardOptions[CustomerRow]{Location: loc, Validate: fn})
//
// 限额固定为 DefaultMaxBytes（10MB）、DefaultMaxRows（5000 行）、
// DefaultMaxErrors（100 条明细错误）与 GuardMaxUnzipBytes（80MB 解压后总大小），
// 不接受参数调整，也不作为请求参数暴露。
type ImportGuard[T any] struct {
	sem chan struct{}
}

// NewImportGuard 创建导入 guard。maxConcurrent 是同时解析的最大导入数，
// <=0 时使用默认值 GuardDefaultConcurrent；超出并发的导入排队等待，而不是失败。
func NewImportGuard[T any](maxConcurrent int) *ImportGuard[T] {
	if maxConcurrent <= 0 {
		maxConcurrent = GuardDefaultConcurrent
	}
	return &ImportGuard[T]{sem: make(chan struct{}, maxConcurrent)}
}

// GuardOptions 是单次导入的可选参数。与 ImportOptions 不同，这里没有
// 任何限额字段：限额由 guard 固定，调用方无法放宽。
type GuardOptions[T any] struct {
	// Sheet 是工作表名；留空时读取第一个工作表。
	Sheet string
	// Location 解析无时区日期文本与 Excel 序列号时使用的时区，nil 时使用
	// time.Local。由调用方传入（业务时区来自时区配置），guard 原样透传给 helper。
	Location *time.Location
	// AllowUnknownHeaders 为 true 时忽略模板之外的列，否则未知列视为错误。
	AllowUnknownHeaders bool
	// Validate 在每行字段解析成功后执行，返回的错误会带上行号汇总。
	// 语义与 ImportOptions.Validate 一致。
	Validate func(row int, v *T) error
}

// Import 用固定限额解析 Excel，行为与 Import 一致：第一行是表头，
// 有错整批拒绝、不返回任何数据。逐行校验失败返回 *ValidationError，
// 其余错误返回 *GuardError；HTTP 状态映射由 HTTP 适配层
// internal/pkg/response 提供，本包保持 HTTP 无关。
func (g *ImportGuard[T]) Import(r io.Reader, opts GuardOptions[T]) ([]T, error) {
	// 应用级信号量：超出并发的导入在此排队等待。
	g.sem <- struct{}{}
	defer func() { <-g.sem }()

	items, err := Import[T](r, ImportOptions[T]{
		Sheet:               opts.Sheet,
		MaxBytes:            DefaultMaxBytes,
		MaxRows:             DefaultMaxRows,
		MaxErrors:           DefaultMaxErrors,
		MaxUnzipBytes:       GuardMaxUnzipBytes,
		Location:            opts.Location,
		AllowUnknownHeaders: opts.AllowUnknownHeaders,
		Validate:            opts.Validate,
	})
	if err != nil {
		return nil, wrapGuardError(err)
	}
	return items, nil
}

// GuardErrorKind 是导入 guard 错误的稳定业务类别。值只增不改，
// 前端与接入方可据此区分「文件太大」「行数太多」「内容有误」。
type GuardErrorKind int

const (
	// GuardErrInternal 未预期错误，对外不泄露内部细节。
	GuardErrInternal GuardErrorKind = iota
	// GuardErrFileTooLarge 文件超过大小上限（含解压后总大小）。
	GuardErrFileTooLarge
	// GuardErrTooManyRows 数据行数超过上限。
	GuardErrTooManyRows
	// GuardErrInvalidWorkbook 文件不是可解析的 xlsx 或缺少表头。
	GuardErrInvalidWorkbook
	// GuardErrSheetNotFound 指定的工作表不存在。
	GuardErrSheetNotFound
	// GuardErrInvalidSchema 字段映射非法（结构体标签或字段类型不支持）。
	GuardErrInvalidSchema
	// GuardErrInvalidHeader 表头不合法（未知列、缺少必填列、重复表头）。
	GuardErrInvalidHeader
)

// GuardError 是 guard 的类型化业务错误，与 net/http 解耦：
// Kind→HTTP 状态的映射由 HTTP 适配层 internal/pkg/response 提供。
// Msg 是稳定的用户可见文案，不包含内部细节；Err 保留底层原因，
// 仅供日志与 errors.Is/As。
type GuardError struct {
	Kind GuardErrorKind
	Msg  string
	Err  error
}

func (e *GuardError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *GuardError) Unwrap() error { return e.Err }

// ValidationError 表示导入逐行校验失败：这是业务结果而非服务端错误。
// 字段与 helper 的 ImportError 对齐，可直接作为响应 data 序列化。
type ValidationError struct {
	Errors    []CellError `json:"errors"`
	Total     int         `json:"total"`
	Truncated bool        `json:"truncated"`
	ie        *ImportError
}

func (e *ValidationError) Error() string {
	if e.Truncated {
		return fmt.Sprintf("导入校验失败（共 %d 处，仅保留前 %d 条）", e.Total, len(e.Errors))
	}
	return fmt.Sprintf("导入校验失败（共 %d 处）", e.Total)
}

// Unwrap 保留 helper 的 *ImportError，兼容按 helper 语义判断的调用方。
func (e *ValidationError) Unwrap() error { return e.ie }

// wrapGuardError 把 helper 错误归类为类型化业务错误。
func wrapGuardError(err error) error {
	switch {
	case errors.Is(err, ErrFileTooLarge):
		return &GuardError{Kind: GuardErrFileTooLarge, Msg: "文件超过大小上限", Err: err}
	case errors.Is(err, ErrTooManyRows):
		return &GuardError{Kind: GuardErrTooManyRows, Msg: "数据超过行数上限", Err: err}
	case errors.Is(err, ErrSheetNotFound):
		return &GuardError{Kind: GuardErrSheetNotFound, Msg: "工作表不存在", Err: err}
	case errors.Is(err, ErrInvalidWorkbook):
		return &GuardError{Kind: GuardErrInvalidWorkbook, Msg: "无法解析的 Excel 文件，请使用导入模板填写", Err: err}
	case errors.Is(err, ErrInvalidSchema), errors.Is(err, ErrUnsupportedType):
		return &GuardError{Kind: GuardErrInvalidSchema, Msg: "字段映射不合法", Err: err}
	}
	var ie *ImportError
	if errors.As(err, &ie) {
		if isHeaderError(ie) {
			return &GuardError{Kind: GuardErrInvalidHeader, Msg: "表头与导入模板不匹配", Err: err}
		}
		return &ValidationError{Errors: ie.Errors, Total: ie.Total, Truncated: ie.Truncated, ie: ie}
	}
	return &GuardError{Kind: GuardErrInternal, Msg: "服务器内部错误", Err: err}
}

// isHeaderError 判断明细错误是否全部来自表头行。
//
// 依赖 Import 的公开契约（见 import.go 的 Import 文档）：第一行必须是
// 表头，数据从第 2 行开始。在该契约下，表头错误（未知列、缺少必填列、
// 重复表头，见 matchHeader）全部记在第 1 行，数据行错误全部记在第 2 行
// 起，且两类错误不会混合在同一次导入里，因此「明细全部在第 1 行」
// 等价于「纯表头错误」。
func isHeaderError(ie *ImportError) bool {
	for _, item := range ie.Errors {
		if item.Row != 1 {
			return false
		}
	}
	return true
}
