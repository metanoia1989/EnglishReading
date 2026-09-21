package store

import "testing"

func TestNormalizeDictText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "literal LF becomes a real line feed",
			in:   `绝对的, 专制的, 完全的, 独立的\nn. 绝对事物`,
			want: "绝对的, 专制的, 完全的, 独立的\nn. 绝对事物",
		},
		{
			name: "literal CRLF becomes one real line feed",
			in:   `第一个字母 A; 一个; 第一的\r\nart. [计] 累加器`,
			want: "第一个字母 A; 一个; 第一的\nart. [计] 累加器",
		},
		{
			name: "lone literal CR becomes a real line feed",
			in:   `a\rb`,
			want: "a\nb",
		},
		{
			name: "several separators in one definition",
			in:   `牢牢抓住, 钉紧\nvt. 紧握, 确定\nvi. 握紧, 钉牢`,
			want: "牢牢抓住, 钉紧\nvt. 紧握, 确定\nvi. 握紧, 钉牢",
		},
		{
			name: "real newlines are preserved",
			in:   "a\nb\r\nc",
			want: "a\nb\r\nc",
		},
		{
			name: "text without escapes is untouched",
			in:   "大牧场, 大农场",
			want: "大牧场, 大农场",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeDictText(tc.in)
			if got != tc.want {
				t.Fatalf("NormalizeDictText(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// The API and the seeder both call this, and rows normalised on
			// write are read back through it again: a second pass must be a
			// no-op or definitions would keep growing line breaks.
			if again := NormalizeDictText(got); again != got {
				t.Fatalf("NormalizeDictText is not idempotent: second pass %q -> %q", got, again)
			}
		})
	}
}
