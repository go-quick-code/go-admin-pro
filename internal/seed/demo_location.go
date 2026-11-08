// demo_location.go 是演示数据种子的业务时区注入口（ADR 0006）：
// seed 包不读配置，app 在 seed.Run 之前经 SetDemoLocation 注入 cfg.Location()；
// 未注入时（单元测试/独立调用）回退确定的 config.DefaultTimezone，绝不依赖
// 进程本地时区（#98 审查 NIT）。独立于 erp_demo.go，保证代码生成器的
// fresh-module 编译校验剪除 erp 演示种子后仍能编译 app.go 的装配行。
package seed

import (
	"time"

	"github.com/go-quick-code/go-admin-pro/internal/config"
)

// demoLocation 是演示数据的业务时区；零值表示尚未注入。
var demoLocation *time.Location

// SetDemoLocation 注入演示数据的业务时区（与 SetDemoStorageBasePath 同款
// 注入方式）；nil 忽略。
func SetDemoLocation(loc *time.Location) {
	if loc != nil {
		demoLocation = loc
	}
}

// demoNow 返回业务时区下的当前时间（注入缺失时回退确定默认时区）。
func demoNow() time.Time {
	loc := demoLocation
	if loc == nil {
		if loaded, err := time.LoadLocation(config.DefaultTimezone); err == nil {
			loc = loaded
		} else {
			loc = time.UTC
		}
	}
	return time.Now().In(loc)
}
