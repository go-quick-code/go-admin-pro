// sensitive_word_trie.go 承载敏感词匹配的核心数据结构（issue #81，
// docs/platform-mvp-spec.md §4.7.2）：rune 前缀树 + 单遍扫描。
//
// 语义约定：
//   - 匹配按 rune 逐位进行，ASCII 字母（A–Z）大小写折叠，非 ASCII（中文等）
//     原样比较——规格只要求「ASCII 不区分大小写」；
//   - 同一位置多词可匹配时取最长（DFA 式敏感词过滤惯例），被最长匹配覆盖的
//     前缀词不单独报告（如词库含 ab/abcd、文本 abcd 只报 abcd）；
//   - Scan 返回去重后的命中词（库词原形，非文本中的大小写变体），按首次出现
//     的 rune 位置排序；打码文本把每个命中 rune 替换为 '*'（rune 级替换，
//     中英文宽度一致，不做半角全角换算）。
//
// 纯数据结构、无 IO 无锁：并发安全由调用方（按租户快照缓存）保证——
// 构建完成后只读，见 sensitive_word.go 的 matcher 缓存。
package service

// maskRune 是打码占位字符（spec §4.7.2：命中字符替换为 *）。
const maskRune = '*'

// sensitiveTrie 是 rune 前缀树的一个节点（兼作树根）。
type sensitiveTrie struct {
	// children 按「折叠后的 rune」索引子节点；根节点的 path 无意义。
	children map[rune]*sensitiveTrie
	// word 非空表示该节点是某个库词的终结节点，存库词原形
	//（Scan 命中时返回它，而不是文本中的大小写变体）。
	word string
}

// newSensitiveTrie 创建空 trie（根节点）。
func newSensitiveTrie() *sensitiveTrie {
	return &sensitiveTrie{children: make(map[rune]*sensitiveTrie)}
}

// asciiLower 只折叠 ASCII 大写字母（spec §4.7.2：ASCII 不区分大小写；
// unicode.ToLower 会把土耳其 İ 等非 ASCII 字符也折叠，超出规格口径）。
func asciiLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}

// Insert 把一个词插入 trie；空串忽略（service 层校验已保证非空，这里兜底）。
func (t *sensitiveTrie) Insert(word string) {
	if word == "" {
		return
	}
	node := t
	for _, r := range word {
		key := asciiLower(r)
		child, ok := node.children[key]
		if !ok {
			child = newSensitiveTrie()
			node.children[key] = child
		}
		node = child
	}
	node.word = word
}

// sensitiveHit 是一次命中的记录：命中词与首次出现的 rune 下标。
type sensitiveHit struct {
	word  string
	start int
}

// Scan 单遍扫描文本：返回去重后的命中词（按首次出现位置升序）与打码文本。
// 复杂度 O(len(text) × 平均词长)，词库规模不影响单点匹配成本。
func (t *sensitiveTrie) Scan(text string) ([]string, string) {
	runes := []rune(text)
	masked := make([]rune, len(runes))
	copy(masked, runes)

	// seen 去重：同一词多次命中只按首次出现入列一次（order 因此天然按
	// 首次出现位置升序）。
	seen := make(map[string]bool)
	var order []string

	for i := 0; i < len(runes); i++ {
		node := t
		end := -1 // 当前起点下最长匹配的终点（开区间）
		var matched *sensitiveTrie
		for j := i; j < len(runes); j++ {
			child, ok := node.children[asciiLower(runes[j])]
			if !ok {
				break
			}
			node = child
			if node.word != "" {
				end = j + 1
				matched = node
			}
		}
		if end < 0 {
			continue // 该位置无任何词命中，前进一位
		}
		// 命中：整段替换为 *，跳过整段继续扫（longest-match 语义）。
		for k := i; k < end; k++ {
			masked[k] = maskRune
		}
		if _, ok := seen[matched.word]; ok {
			i = end - 1
			continue
		}
		seen[matched.word] = true
		order = append(order, matched.word)
		i = end - 1 // for 循环自增后落到 end
	}

	// order 在扫描时按首现位置依次追加（每词只在首现时入列），即「按首次
	// 出现位置排序」的结果，无需再排。
	return order, string(masked)
}
