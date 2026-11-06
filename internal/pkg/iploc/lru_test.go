package iploc

import (
	"fmt"
	"sync"
	"testing"
)

// TestCacheCapacityEviction 容量语义（issue #82，spec §4.8.5）：
// 缓存超过容量时按 LRU 淘汰最久未使用的条目。
func TestCacheCapacityEviction(t *testing.T) {
	c := NewCache(2)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Set("c", "3") // 满容量插入 → 淘汰最久未用的 a

	if v := c.Get("a"); v != "" {
		t.Fatalf("a 应已被淘汰，实际命中 %q", v)
	}
	if v := c.Get("b"); v != "2" {
		t.Fatalf("b 应命中 2，实际 %q", v)
	}
	if v := c.Get("c"); v != "3" {
		t.Fatalf("c 应命中 3，实际 %q", v)
	}
}

// TestCacheGetRefreshesRecency 命中会刷新新鲜度：
// 先 Get 的条目在淘汰时优先出局，刚命中的条目保留。
func TestCacheGetRefreshesRecency(t *testing.T) {
	c := NewCache(2)
	c.Set("a", "1")
	c.Set("b", "2")
	if v := c.Get("a"); v != "1" { // a 变为最近使用，b 成为最久未用
		t.Fatalf("a 应命中，实际 %q", v)
	}
	c.Set("c", "3") // 淘汰 b
	if v := c.Get("b"); v != "" {
		t.Fatalf("b 应已被淘汰，实际命中 %q", v)
	}
	if v := c.Get("a"); v != "1" {
		t.Fatalf("a 应保留，实际 %q", v)
	}
}

// TestCacheSetRefreshesValue 同 key 重复 Set 覆盖值并刷新新鲜度。
func TestCacheSetRefreshesValue(t *testing.T) {
	c := NewCache(2)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Set("a", "9") // a 覆盖值且变为最近使用
	if v := c.Get("a"); v != "9" {
		t.Fatalf("a 应命中 9，实际 %q", v)
	}
	c.Set("c", "3") // 淘汰 b（a 刚被刷新）
	if v := c.Get("b"); v != "" {
		t.Fatalf("b 应已被淘汰，实际命中 %q", v)
	}
}

// TestCacheZeroCapacity 容量 0 退化：不缓存任何条目（防御性语义）。
func TestCacheZeroCapacity(t *testing.T) {
	c := NewCache(0)
	c.Set("a", "1")
	if v := c.Get("a"); v != "" {
		t.Fatalf("容量 0 不应缓存，实际命中 %q", v)
	}
}

// TestCacheConcurrent 并发安全：-race 下多 goroutine 读写不 panic、不丢写。
func TestCacheConcurrent(t *testing.T) {
	c := NewCache(128)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				key := fmt.Sprintf("k%d", (n*j)%256)
				c.Set(key, fmt.Sprintf("v%d", n))
				c.Get(key)
			}
		}(i)
	}
	wg.Wait()
}
