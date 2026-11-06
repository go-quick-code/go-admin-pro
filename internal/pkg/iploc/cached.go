package iploc

// LoginLogCacheSize 是登录日志归属地缓存的容量（spec §4.8.5：10000 条 LRU）。
const LoginLogCacheSize = 10000

// NewCached 用 LRU 缓存装饰 base：同 IP 只回源一次。
// 容量 capacity <= 0 时直接返回 base（不缓存）。
// 空 IP 不回源也不占缓存（登录日志存在无 IP 的行）。
func NewCached(base Resolver, capacity int) Resolver {
	if capacity <= 0 {
		return base
	}
	return &cachedResolver{base: base, cache: NewCache(capacity)}
}

// cachedResolver 是带 LRU 的 Resolver 装饰器。
type cachedResolver struct {
	base  Resolver
	cache *Cache
}

// Resolve 实现 Resolver。
func (r *cachedResolver) Resolve(ip string) string {
	if ip == "" {
		return ""
	}
	if v := r.cache.Get(ip); v != "" {
		return v
	}
	v := r.base.Resolve(ip)
	if v != "" {
		r.cache.Set(ip, v)
	}
	return v
}
