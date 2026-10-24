package alignio

import "testing"

func TestAlignUp(t *testing.T) {
	cases := []struct{ n, align, want int }{
		{0, 4, 0}, {1, 4, 4}, {3, 4, 4}, {4, 4, 4}, {5, 4, 8},
		{0, 8, 0}, {7, 8, 8}, {8, 8, 8}, {9, 8, 16},
		{6, 2, 6}, {7, 2, 8}, {3, 1, 3},
	}
	for _, c := range cases {
		if got := AlignUp(c.n, c.align); got != c.want {
			t.Errorf("AlignUp(%d,%d) = %d, want %d", c.n, c.align, got, c.want)
		}
		if got := Pad(c.n, c.align); got != c.want-c.n {
			t.Errorf("Pad(%d,%d) = %d, want %d", c.n, c.align, got, c.want-c.n)
		}
	}
}

func TestPadTo(t *testing.T) {
	if got := PadTo([]byte("abc"), 4); len(got) != 4 || string(got) != "abc\x00" {
		t.Errorf("PadTo(abc,4) = %q, want abc\\x00", got)
	}
	if got := PadTo([]byte("abcd"), 4); len(got) != 4 {
		t.Errorf("PadTo(abcd,4) = %q, want unchanged", got)
	}
	if got := PadTo(nil, 8); len(got) != 0 {
		t.Errorf("PadTo(nil,8) = %q, want empty", got)
	}
}
