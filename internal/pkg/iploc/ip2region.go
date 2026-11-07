package iploc

import (
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
)

// xdb 头部（256B）内的字段偏移：起始/结束索引指针（小端 uint32），
// 之后是 256*256*8 的向量索引区。加载前据此做最小完整性校验，
// 避免把截断/损坏文件交给检索器后在查询时 panic。
const (
	xdbHeaderLen   = 256
	xdbVectorLen   = 256 * 256 * 8
	xdbStartPtrOff = 8
	xdbEndPtrOff   = 12
)

// ip2RegionResolver 是 ip2region xdb v2 驱动（可选，配置 ip-location.xdb-path
// 时启用，spec §4.8.4）。xdb 文件由部署侧提供，本仓库不内置数据文件。
//
// 官方 xdb.Searcher 明确「not thread safe」（Search 会改写内部 ioCount），
// 而登录日志分页是并发请求，因此整个文件加载进内存（NewWithBuffer）并加
// 互斥锁串行查询；配合 CachedResolver 的 LRU，锁竞争可忽略。
type ip2RegionResolver struct {
	mu       sync.Mutex
	searcher *xdb.Searcher
}

// NewIP2Region 加载 xdb 文件构造驱动。加载失败显式返回错误
// （由 New 工厂兜底转告警 + 空实现，构造函数本身不吞错误）。
func NewIP2Region(xdbPath string) (Resolver, error) {
	content, err := xdb.LoadContentFromFile(xdbPath)
	if err != nil {
		return nil, fmt.Errorf("读取 xdb 文件: %w", err)
	}
	if err := validateXdbContent(content); err != nil {
		return nil, fmt.Errorf("xdb 内容非法: %w", err)
	}
	// 头部偏移 16 的 IP 版本字段：旧版（IPv4-only）文件该处为 0，
	// 新版 maker 写 4/6；0 与 4 都按 IPv4 处理。
	version := xdb.IPv4
	if len(content) >= 18 {
		if v := int(content[16]) | int(content[17])<<8; v == 6 {
			version = xdb.IPv6
		}
	}
	searcher, err := xdb.NewWithBuffer(version, content)
	if err != nil {
		return nil, fmt.Errorf("解析 xdb 内容: %w", err)
	}
	return &ip2RegionResolver{searcher: searcher}, nil
}

// validateXdbContent 校验 xdb 内容的最小完整性：长度必须覆盖头部 + 向量
// 索引区，段索引起止指针必须落在向量索引区之后、文件范围内。
// 检索器对越界指针不做防御（会 panic），必须在这里拦下。
func validateXdbContent(content []byte) error {
	if len(content) < xdbHeaderLen+xdbVectorLen {
		return fmt.Errorf("文件过小（%d 字节），头部与向量索引区要求 %d 字节",
			len(content), xdbHeaderLen+xdbVectorLen)
	}
	startPtr := binary.LittleEndian.Uint32(content[xdbStartPtrOff:])
	endPtr := binary.LittleEndian.Uint32(content[xdbEndPtrOff:])
	minIndex := uint32(xdbHeaderLen + xdbVectorLen)
	if startPtr < minIndex || endPtr < startPtr || endPtr >= uint32(len(content)) {
		return fmt.Errorf("段索引指针越界（start=%d end=%d 文件=%d 字节）",
			startPtr, endPtr, len(content))
	}
	return nil
}

// Resolve 实现 Resolver：成功返回格式化归属地；IP 非法或检索失败返回空串
// （spec §4.8.5「定位失败返回空，不影响分页」）。检索器对个别越界数据
// 不做防御，这里用 recover 兜底：任何 panic 都视为「该 IP 定位失败」。
func (r *ip2RegionResolver) Resolve(ip string) (loc string) {
	if ip == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		if recover() != nil {
			loc = ""
		}
	}()
	region, err := r.searcher.Search(ip)
	if err != nil {
		return ""
	}
	return FormatRegion(region)
}
