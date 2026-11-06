package text

import "testing"

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name  string
		value string
		max   int
		want  string
	}{
		{name: "不超长原样返回", value: "你好world", max: 10, want: "你好world"},
		{name: "按字符而非字节截断", value: "你好世界", max: 2, want: "你好"},
		{name: "恰好等于上限", value: "abc", max: 3, want: "abc"},
		{name: "零上限返回空", value: "abc", max: 0, want: ""},
		{name: "负上限返回空", value: "abc", max: -1, want: ""},
		{name: "空串", value: "", max: 5, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TruncateRunes(tc.value, tc.max); got != tc.want {
				t.Fatalf("TruncateRunes(%q, %d) = %q，期望 %q", tc.value, tc.max, got, tc.want)
			}
		})
	}
}
