package service

import (
	"reflect"
	"testing"
)

// 敏感词 rune trie 单元测试（issue #81，spec §4.7.2）：命中/打码、ASCII
// 不区分大小写、去重、按首次出现位置排序、最长匹配。停用词不参与匹配由
// service 层保证（停用词不插入 trie，见 sensitive_word.go），这里验证
// 「不插入即不命中」的 trie 基础语义。

// TestSensitiveTrieHitAndMask 命中返回词表与打码文本：命中字符逐 rune 替换 *。
func TestSensitiveTrieHitAndMask(t *testing.T) {
	trie := newSensitiveTrie()
	trie.Insert("敏感词")
	trie.Insert("bad")

	words, masked := trie.Scan("这是一条敏感词记录，really BAD!")
	if !reflect.DeepEqual(words, []string{"敏感词", "bad"}) {
		t.Fatalf("命中词 = %v，期望 [敏感词 bad]", words)
	}
	want := "这是一条***记录，really ***!"
	if masked != want {
		t.Fatalf("打码文本 = %q，期望 %q", masked, want)
	}
}

// TestSensitiveTrieASCIICaseInsensitive ASCII 大小写折叠：库词以原始形式返回。
func TestSensitiveTrieASCIICaseInsensitive(t *testing.T) {
	trie := newSensitiveTrie()
	trie.Insert("Bad")
	trie.Insert("赌博")

	words, masked := trie.Scan("xBaDx赌博y")
	if !reflect.DeepEqual(words, []string{"Bad", "赌博"}) {
		t.Fatalf("命中词 = %v，期望库词原形 [Bad 赌博]", words)
	}
	if masked != "x***x**y" {
		t.Fatalf("打码文本 = %q，期望 %q", masked, "x***x**y")
	}
}

// TestSensitiveTrieDedupAndPositionOrder 去重 + 按首次出现位置排序：
// 同词多次命中只报一次；插入顺序不影响返回顺序。
func TestSensitiveTrieDedupAndPositionOrder(t *testing.T) {
	trie := newSensitiveTrie()
	trie.Insert("后出现的词")
	trie.Insert("先出现的")

	words, _ := trie.Scan("先出现的 ... 后出现的词 ... 先出现的")
	if !reflect.DeepEqual(words, []string{"先出现的", "后出现的词"}) {
		t.Fatalf("命中词 = %v，期望按首现位置 [先出现的 后出现的词]", words)
	}
}

// TestSensitiveTrieLongestMatch 同一位置多词可匹配时取最长：命中 longest，
// 其前缀不再单独报告。
func TestSensitiveTrieLongestMatch(t *testing.T) {
	trie := newSensitiveTrie()
	trie.Insert("ab")
	trie.Insert("abcd")

	words, masked := trie.Scan("xabcdx abx")
	if !reflect.DeepEqual(words, []string{"abcd", "ab"}) {
		t.Fatalf("命中词 = %v，期望 [abcd ab]", words)
	}
	if masked != "x****x **x" {
		t.Fatalf("打码文本 = %q，期望 %q", masked, "x****x **x")
	}
}

// TestSensitiveTrieNoHit 无命中：词表为空、文本原样返回。
func TestSensitiveTrieNoHit(t *testing.T) {
	trie := newSensitiveTrie()
	trie.Insert("敏感词")

	words, masked := trie.Scan("正常内容")
	if len(words) != 0 {
		t.Fatalf("命中词 = %v，期望空", words)
	}
	if masked != "正常内容" {
		t.Fatalf("打码文本 = %q，期望原样", masked)
	}

	// 空文本与空词库同样安全。
	if w, m := trie.Scan(""); len(w) != 0 || m != "" {
		t.Fatalf("空文本应返回空结果，得到 %v %q", w, m)
	}
	if w, m := newSensitiveTrie().Scan("任意"); len(w) != 0 || m != "任意" {
		t.Fatalf("空词库不应命中，得到 %v %q", w, m)
	}
}

// TestSensitiveTrieStopwordNotInserted 停用词不参与匹配（spec §4.7.3）：
// trie 只装启用词，未插入的词不命中。
func TestSensitiveTrieStopwordNotInserted(t *testing.T) {
	trie := newSensitiveTrie()
	trie.Insert("启用词") // 停用词「停用词」不入 trie

	words, _ := trie.Scan("停用词 与 启用词")
	if !reflect.DeepEqual(words, []string{"启用词"}) {
		t.Fatalf("命中词 = %v，期望只命中启用词", words)
	}
}

// TestSensitiveTrieAdjacentMatches 相邻命中互不吞并：前一个命中的终点之后
// 立即开始下一段匹配。
func TestSensitiveTrieAdjacentMatches(t *testing.T) {
	trie := newSensitiveTrie()
	trie.Insert("aa")
	trie.Insert("bb")

	words, masked := trie.Scan("aabb")
	if !reflect.DeepEqual(words, []string{"aa", "bb"}) {
		t.Fatalf("命中词 = %v，期望 [aa bb]", words)
	}
	if masked != "****" {
		t.Fatalf("打码文本 = %q，期望 ****", masked)
	}
}
