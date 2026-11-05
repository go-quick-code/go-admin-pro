package response

import (
	"errors"
	"net/http"

	"github.com/go-quick-code/go-admin-pro/internal/pkg/excel"
)

// ImportGuardHTTPStatus 返回 excel 导入 guard 错误对应的 HTTP 状态码：
// 逐行校验失败（*excel.ValidationError，业务结果而非服务端错误）为 200；
// 限额与内容错误（*excel.GuardError，除 GuardErrInternal 外）为 400；
// GuardErrInternal 与其他非 guard 错误一律为 500。
//
// 这是 Kind→HTTP 的唯一映射点：excel 包保持 HTTP 无关（见
// CODING_STANDARDS.md「包与边界」）。excel.GuardErrorKind 的值稳定
// 只增不改（见其文档），且映射只按「是否 GuardErrInternal」分流，
// 新增 Kind 时无需改动本函数即落入 400 分支。
func ImportGuardHTTPStatus(err error) int {
	var ve *excel.ValidationError
	if errors.As(err, &ve) {
		return http.StatusOK
	}
	var ge *excel.GuardError
	if errors.As(err, &ge) {
		if ge.Kind == excel.GuardErrInternal {
			return http.StatusInternalServerError
		}
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
