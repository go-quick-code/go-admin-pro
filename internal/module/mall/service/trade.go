package service

import (
	"sync/atomic"
	"time"
)

// trade.go 是交易域的业务时钟（issue #179，docs/mall-mvp-spec.md §6 验收
// 「照 pay 先例建 mall SetBusinessClock atomic.Value 缝，注释对齐」）：SO 单号
// 日期键与本模块一切业务时间判定（T9 超时关单扫描、关单时间戳）的统一来源。

// businessClock 是 mall 业务时间（单号日期键/订单超时/关单时间戳等）的来源。
// 用 var 而非直接 time.Now()：仅集成测试注入固定时钟验证超时关单时间轴
// （用例内复原），生产代码不写此变量。atomic.Value 存 func() time.Time：
// 拨钟 swap 与调度器 goroutine 的读并发（T9 超时关单 tick 经
// businessNow），atomic 消除 data race（照 pay service.go 逐字同型）。
var businessClock atomic.Value

func init() { businessClock.Store(time.Now) }

// SetBusinessClock 是业务时钟的写缝：注入固定时钟并返回原值，供集成测试
// 在用例内替换与复原（同 message SetQuotaClock / pay SetBusinessClock 的
// 定位），生产代码不应调用。
func SetBusinessClock(f func() time.Time) func() time.Time {
	prev := businessClock.Load().(func() time.Time)
	businessClock.Store(f)
	return prev
}

// SetLocation 注入业务时区（ADR 0006）：单号日期键的唯一口径（装配期由
// app.New 经 SetLocation 注入，照 pay SetLocation 先例）。
func (s *Service) SetLocation(loc *time.Location) {
	if loc != nil {
		s.location = loc
	}
}

// businessLocation 返回业务时区；未注入时退回进程本地时区（测试直连场景）。
func (s *Service) businessLocation() *time.Location {
	if s.location != nil {
		return s.location
	}
	return time.Local
}

// businessNow 返回业务时区下的「现在」：单号日期键一律走这里，不直接用
// time.Now()（其 Location 是服务器本地时区，跨时区部署会算错日期键）。
func (s *Service) businessNow() time.Time {
	return businessClock.Load().(func() time.Time)().In(s.businessLocation())
}
