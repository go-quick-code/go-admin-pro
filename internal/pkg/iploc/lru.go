package iploc

import (
	"container/list"
	"sync"
)

// Cache 是一个固定容量的线程安全 LRU 缓存（issue #82，spec §4.8.5）：
// 登录日志分页补归属地时按 IP 缓存解析结果，避免每次分页都对同一批
// IP 重复走 xdb 检索；容量淘汰最久未使用的条目。
type Cache struct {
	mu       sync.Mutex
	capacity int
	// ll 前端是最近使用的条目，后端是最久未使用的条目；
	// 元素值为 entry{key, value}，淘汰时需要 key 反删 map。
	ll    *list.List
	items map[string]*list.Element
}

type cacheEntry struct {
	key   string
	value string
}

// NewCache 创建容量为 capacity 的 LRU 缓存；capacity <= 0 时退化为不缓存。
func NewCache(capacity int) *Cache {
	return &Cache{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[string]*list.Element),
	}
}

// Get 返回 key 对应的值；未命中返回空串。命中会把条目移到最新位置。
func (c *Cache) Get(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(cacheEntry).value
	}
	return ""
}

// Set 写入/覆盖 key，并把条目移到最新位置；超过容量时淘汰最久未使用的条目。
func (c *Cache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.capacity <= 0 {
		return
	}
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		el.Value = cacheEntry{key: key, value: value}
		return
	}
	c.items[key] = c.ll.PushFront(cacheEntry{key: key, value: value})
	if c.ll.Len() > c.capacity {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(cacheEntry).key)
		}
	}
}
