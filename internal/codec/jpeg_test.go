package codec

import (
	"bytes"
	"context"
	"image"
	"io"
	"testing"

	"github.com/dobbo-ca/jpeg-go"
)

// Every jpeg-go call is bounded by the band budget, never run with zero Limits.
func TestJPEGCallLimited(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(context.Background(), &buf, image.NewGray(image.Rect(0, 0, 64, 64)), nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	r, err := newJPEG(context.Background(), io.NewSectionReader(bytes.NewReader(b), 0, int64(len(b))), Params{SPP: 1, W: 64, H: 64, BandBytes: 64 * 64})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.(*jpegRows).opts.Limits.MaxBytes, jpegBudget(64*64); got != want {
		t.Fatalf("MaxBytes %d, want %d", got, want)
	}
}

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
		"baseline interleaved":        {cat(sof(0xc0, 3), sos(3)), true},
		"baseline gray":               {cat(sof(0xc0, 1), sos(1)), true},
		"baseline non-interleaved":    {cat(sof(0xc0, 3), sos(1)), false},
		"progressive":                 {cat(sof(0xc2, 3), sos(3)), false},
		"fill before SOF2":            {cat([]byte{0xff}, sof(0xc2, 3), sos(3)), false},
		"fill before SOF0":            {cat([]byte{0xff, 0xff}, sof(0xc0, 3), sos(3)), true},
		"app then baseline":           {cat([]byte{0xff, 0xe0, 0, 4, 1, 2}, sof(0xc0, 3), sos(3)), true},
		"RST length hides fake frame": {cat([]byte{0xff, 0xd0, 0, 6, 0xff, 0xe1, 0, 0x20}, sof(0xc0, 3), sos(3), make([]byte, 8), sof(0xc2, 3), sos(1)), false},
		"no frame":                    {cat(sos(1)), false},
		"truncated":                   {cat(sof(0xc0, 3)[:6]), false},
	} {
		if got := bandable(c.b); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
