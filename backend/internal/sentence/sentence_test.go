package sentence

import "testing"

func TestSplit(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"One. Two! Three?", 3},
		{"Mr. Holmes walked in. He sat down.", 2},
		{"The value is 3.14 today. Next sentence.", 2},
		{"\"What a fool I am,\" he said. \"Here I am,\" he added.", 2},
	}
	for _, c := range cases {
		got := Split(c.in)
		if len(got) != c.want {
			t.Errorf("Split(%q) = %d sentences (%v), want %d", c.in, len(got), got, c.want)
		}
	}
}
