package iploc

import (
	"encoding/binary"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// capturingLogger 捕获日志输出（每条一行），用于断言「加载失败只告警一次」。
type capturingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *capturingLogger) Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&lineWriter{l: l}, nil))
}

func (l *capturingLogger) Messages() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

type lineWriter struct{ l *capturingLogger }

func (w *lineWriter) Write(p []byte) (int, error) {
	w.l.mu.Lock()
	defer w.l.mu.Unlock()
	w.l.lines = append(w.l.lines, string(p))
	return len(p), nil
}

// TestNoopResolver 空实现：任何 IP 都返回空串（spec §4.8.4 未配置 xdb 时）。
func TestNoopResolver(t *testing.T) {
	r := NewNoop()
	for _, ip := range []string{"1.2.3.4", "", "not-an-ip", "8.8.8.8"} {
		if v := r.Resolve(ip); v != "" {
			t.Fatalf("空实现 Resolve(%q) 应为空串，实际 %q", ip, v)
		}
	}
}

// TestNewEmptyPathNoWarn 未配置 xdb-path：直接空实现，无任何告警。
func TestNewEmptyPathNoWarn(t *testing.T) {
	cap := &capturingLogger{}
	r := New(cap.Logger(), "")
	if r == nil {
		t.Fatal("工厂应返回非 nil resolver")
	}
	if v := r.Resolve("1.2.3.4"); v != "" {
		t.Fatalf("未配置 xdb 时应返回空串，实际 %q", v)
	}
	if msgs := cap.Messages(); len(msgs) != 0 {
		t.Fatalf("未配置 xdb 不应告警，实际 %d 条：%v", len(msgs), msgs)
	}
}

// TestNewMissingXdbWarnsOnce xdb 文件缺失：告警一次并退回空实现，
// 不影响启动（Resolve 不再报错、不再追加告警）。
func TestNewMissingXdbWarnsOnce(t *testing.T) {
	cap := &capturingLogger{}
	r := New(cap.Logger(), filepath.Join(t.TempDir(), "missing.xdb"))
	if r == nil {
		t.Fatal("加载失败应退回空实现而非 panic")
	}
	if v := r.Resolve("1.2.3.4"); v != "" {
		t.Fatalf("退回空实现后 Resolve 应为空串，实际 %q", v)
	}
	// 多次 Resolve 后告警仍只有一条（一次加载、一次告警）。
	for i := 0; i < 5; i++ {
		r.Resolve("1.2.3.4")
	}
	msgs := cap.Messages()
	if len(msgs) != 1 {
		t.Fatalf("xdb 缺失应告警且仅告警一次，实际 %d 条：%v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "missing.xdb") {
		t.Fatalf("告警应包含路径信息，实际 %q", msgs[0])
	}
}

// TestNewCorruptXdbWarnsOnce xdb 内容损坏（截断文件）：同样告警一次退回空实现。
func TestNewCorruptXdbWarnsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.xdb")
	if err := os.WriteFile(path, []byte("not an xdb"), 0o600); err != nil {
		t.Fatal(err)
	}
	cap := &capturingLogger{}
	r := New(cap.Logger(), path)
	if v := r.Resolve("1.2.3.4"); v != "" {
		t.Fatalf("损坏 xdb 应退回空实现，实际 %q", v)
	}
	if msgs := cap.Messages(); len(msgs) != 1 {
		t.Fatalf("损坏 xdb 应告警一次，实际 %d 条", len(msgs))
	}
}

// TestFormatRegion 把 ip2region 的「国家|区域|省份|城市|ISP」格式化为展示文本：
// 过滤空段与占位 0，空段分隔。
func TestFormatRegion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"中国|0|广东省|深圳市|电信", "中国 广东省 深圳市 电信"},
		{"0|0|0|0|0", ""},
		{"", ""},
		{"美国|0|0|0|0", "美国"},
		{"中国|0|浙江省|杭州市|阿里云", "中国 浙江省 杭州市 阿里云"},
	}
	for _, c := range cases {
		if got := FormatRegion(c.in); got != c.want {
			t.Fatalf("FormatRegion(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// buildMiniXdb 构造一个合法的极小 ip2region xdb（v2 结构）：
// 单个网段 0.0.0.0/0 → region 字符串，用于驱动测试（不引入任何真实数据文件）。
//
// 布局（字节偏移，均小端）：
//
//	[0, 256)      头部：版本 2 / 索引策略 1 / 起止索引指针 / IP 版本 4
//	[256, 256+256*256*8) 向量索引：cell = (首字节*256+次字节)*8，4B 起始指针 + 4B 结束指针
//	之后           段索引（IPv4 每条 14B：起 IP / 止 IP / 数据长度 / 数据指针）+ 区域数据
func buildMiniXdb(t *testing.T, region string) string {
	t.Helper()

	const headerLen = 256
	vectorLen := 256 * 256 * 8
	segIndexLen := 14
	segPtr := uint32(headerLen + vectorLen)
	dataPtr := segPtr + uint32(segIndexLen)

	regionBytes := []byte(region)
	buf := make([]byte, int(dataPtr)+len(regionBytes))

	// 头部：version=2, indexPolicy=1(向量), createdAt=0, start/endIndexPtr=segPtr,
	// ipVersion=4, runtimePtrBytes=4（新版本头部字段，旧文件该处为 0，按 IPv4 处理）。
	binary.LittleEndian.PutUint16(buf[0:], 2)
	binary.LittleEndian.PutUint16(buf[2:], 1)
	binary.LittleEndian.PutUint32(buf[8:], segPtr)
	binary.LittleEndian.PutUint32(buf[12:], segPtr) // 单条目：结束指针 = 最后一条起始
	binary.LittleEndian.PutUint16(buf[16:], 4)
	binary.LittleEndian.PutUint16(buf[18:], 4)

	// 向量索引：段覆盖 0.0.0.0-255.255.255.255，全部 cell 都指向该段。
	for i := 0; i < 256*256; i++ {
		off := headerLen + i*8
		binary.LittleEndian.PutUint32(buf[off:], segPtr)
		binary.LittleEndian.PutUint32(buf[off+4:], segPtr)
	}

	// 段索引：起 IP 与止 IP 按 xdb 约定以小端字节序存储。
	copy(buf[segPtr:segPtr+4], []byte{0, 0, 0, 0})
	copy(buf[segPtr+4:segPtr+8], []byte{0xFF, 0xFF, 0xFF, 0xFF})
	binary.LittleEndian.PutUint16(buf[segPtr+8:], uint16(len(regionBytes)))
	binary.LittleEndian.PutUint32(buf[segPtr+10:], dataPtr)
	copy(buf[dataPtr:], regionBytes)

	path := filepath.Join(t.TempDir(), "mini.xdb")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestIP2RegionResolver 驱动接线：极小自制 xdb → 解析出格式化归属地；
// 非法 IP / 解析失败返回空串（spec §4.8.5 定位失败不影响分页）。
func TestIP2RegionResolver(t *testing.T) {
	path := buildMiniXdb(t, "中国|0|广东省|深圳市|电信")
	r, err := NewIP2Region(path)
	if err != nil {
		t.Fatalf("加载自制 xdb: %v", err)
	}
	if got := r.Resolve("88.88.88.88"); got != "中国 广东省 深圳市 电信" {
		t.Fatalf("Resolve(88.88.88.88) = %q，期望 %q", got, "中国 广东省 深圳市 电信")
	}
	// 非法 IP：返回空串而非错误。
	if got := r.Resolve("not-an-ip"); got != "" {
		t.Fatalf("非法 IP 应返回空串，实际 %q", got)
	}
	if got := r.Resolve(""); got != "" {
		t.Fatalf("空 IP 应返回空串，实际 %q", got)
	}
}

// TestIP2RegionResolverConcurrent 驱动并发安全：xdb.Searcher 非线程安全
// （ioCount 等内部状态），驱动层必须串行化。
func TestIP2RegionResolverConcurrent(t *testing.T) {
	path := buildMiniXdb(t, "中国|0|广东省|深圳市|电信")
	r, err := NewIP2Region(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if got := r.Resolve("88.88.88.88"); got != "中国 广东省 深圳市 电信" {
					t.Errorf("并发解析结果错误: %q", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestNewIP2RegionMissingFile 直接构造驱动时文件缺失应显式报错
// （由工厂兜底转告警；构造函数本身不吞错误）。
func TestNewIP2RegionMissingFile(t *testing.T) {
	if _, err := NewIP2Region(filepath.Join(t.TempDir(), "nope.xdb")); err == nil {
		t.Fatal("文件缺失应返回错误")
	}
}

// TestCachedResolver 带 LRU 的装饰器：同 IP 只回源一次。
func TestCachedResolver(t *testing.T) {
	counts := map[string]int{}
	base := ResolverFunc(func(ip string) string {
		counts[ip]++
		return "loc-" + ip
	})
	cached := NewCached(base, 16)
	for i := 0; i < 3; i++ {
		if got := cached.Resolve("1.2.3.4"); got != "loc-1.2.3.4" {
			t.Fatalf("缓存解析结果错误: %q", got)
		}
	}
	if counts["1.2.3.4"] != 1 {
		t.Fatalf("同 IP 应只回源一次，实际 %d 次", counts["1.2.3.4"])
	}
	// 空 IP 不进缓存（登录日志无 IP 的行不值得占容量）。
	if got := cached.Resolve(""); got != "" {
		t.Fatalf("空 IP 应返回空串，实际 %q", got)
	}
}
