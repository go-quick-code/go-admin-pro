package config

import (
	"net/url"
	"strings"
)

// ApplyDSNLoc 返回把 MySQL DSN 的 loc 参数设置为 timezone 的结果。
//
// 规则：DSN 已有 loc 参数时覆盖其值，没有时追加；时区名做 URL 编码
// （如 Asia/Shanghai → Asia%2FShanghai），其余参数字节原样保留。
// dsn 或 timezone 为空时原样返回，不改写。
//
// 参数段按 go-sql-driver 的解析规则定位：最后一个 / 之后的第一个 ?。
// 由 config.Load 在加载阶段调用，使 DSN 的 loc 与 app.timezone 保持一致，
// 服务器与 migrate 命令共用同一份 DSN，不再依赖 time.Local。
func ApplyDSNLoc(dsn, timezone string) string {
	if dsn == "" || timezone == "" {
		return dsn
	}

	encoded := url.QueryEscape(timezone)

	head := dsn
	params := ""
	slash := strings.LastIndex(dsn, "/")
	rest := ""
	if slash >= 0 {
		rest = dsn[slash:]
	} else {
		rest = dsn
	}
	if q := strings.Index(rest, "?"); q >= 0 {
		if slash >= 0 {
			head = dsn[:slash+q]
			params = rest[q+1:]
		} else {
			head = dsn[:q]
			params = rest[q+1:]
		}
	}

	var kept []string
	hasLoc := false
	if params != "" {
		for _, kv := range strings.Split(params, "&") {
			key, _, hasEq := strings.Cut(kv, "=")
			if hasEq && key == "loc" {
				if !hasLoc {
					// 第一个 loc 覆盖为新值，后续 loc 丢弃。
					kept = append(kept, "loc="+encoded)
					hasLoc = true
				}
				continue
			}
			kept = append(kept, kv)
		}
	}
	if !hasLoc {
		kept = append(kept, "loc="+encoded)
	}
	return head + "?" + strings.Join(kept, "&")
}
