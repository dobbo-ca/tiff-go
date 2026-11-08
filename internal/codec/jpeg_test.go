package codec

import "testing"

func TestBandable(t *testing.T) {
	sof := func(m byte, n int) []byte {
		return append([]byte{0xff, m, 0, 8 + 3*byte(n), 8, 0, 8, 0, 8, byte(n)}, make([]byte, 3*n)...)
	}
	sos := func(n int) []byte { return []byte{0xff, 0xda, 0, 6, byte(n)} }
	cat := func(parts ...[]byte) []byte {
		b := []byte{0xff, 0xd8}
		for _, p := range parts {
			b = append(b, p...)
		}
		return b
	}
	for name, c := range map[string]struct {
		b    []byte
		want bool
	}{
		"baseline interleaved":     {cat(sof(0xc0, 3), sos(3)), true},
		"baseline gray":            {cat(sof(0xc0, 1), sos(1)), true},
		"baseline non-interleaved": {cat(sof(0xc0, 3), sos(1)), false},
		"progressive":              {cat(sof(0xc2, 3), sos(3)), false},
		"fill before SOF2":         {cat([]byte{0xff}, sof(0xc2, 3), sos(3)), false},
		"fill before SOF0":         {cat([]byte{0xff, 0xff}, sof(0xc0, 3), sos(3)), true},
		"app then baseline":        {cat([]byte{0xff, 0xe0, 0, 4, 1, 2}, sof(0xc0, 3), sos(3)), true},
		"no frame":                 {cat(sos(1)), false},
		"truncated":                {cat(sof(0xc0, 3)[:6]), false},
	} {
		if got := bandable(c.b); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
