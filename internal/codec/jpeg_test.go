package codec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"testing"

	"github.com/dobbo-ca/jpeg-go"

	"github.com/dobbo-ca/tiff-go/internal/e"
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

// A jpeg-go budget refusal is a limit, not corruption (kleio parks them apart).
func TestJPEGLimitIsNotCorrupt(t *testing.T) {
	const w, h = 2048, 2048
	// SOI, SOF2, SOS: a progressive frame with no entropy data. The row reader reserves
	// its coefficients at SOF, before any data, and the process limit refuses them.
	defer jpeg.SetMemoryLimit(jpeg.SetMemoryLimit(1 << 20))
	b := []byte{0xff, 0xd8, 0xff, 0xc2, 0, 17, 8, h >> 8, h & 255, w >> 8, w & 255, 3, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0, 0xff, 0xda, 0, 12, 3, 1, 0, 2, 0, 3, 0, 0, 0, 0}
	p := Params{SPP: 3, W: w, H: h, BandBytes: 4 * w * h}
	r, err := newJPEG(context.Background(), io.NewSectionReader(bytes.NewReader(b), 0, int64(len(b))), p)
	if err == nil {
		err = r.ReadRow(make([]byte, 3*w))
	}
	var le *e.LimitError
	if !errors.As(err, &le) || errors.Is(err, e.ErrCorrupt) {
		t.Fatalf("got %v, want *LimitError", err)
	}
}

// jpeg-go's process memory limit and 1 GiB cap are limits too, not corruption;
// a decode refused because other decodes hold the budget is transient, so it is neither.
func TestJPEGProcessLimitIsNotCorrupt(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(context.Background(), &buf, image.NewGray(image.Rect(0, 0, 256, 256)), nil); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	p := Params{SPP: 1, W: 256, H: 256, BandBytes: 256 * 256}
	prev := jpeg.SetMemoryLimit(1024)
	defer jpeg.SetMemoryLimit(prev)
	r, err := newJPEG(context.Background(), io.NewSectionReader(bytes.NewReader(b), 0, int64(len(b))), p)
	if err == nil {
		err = r.ReadRow(make([]byte, 256))
	}
	var le *e.LimitError
	if !errors.As(err, &le) || errors.Is(err, e.ErrCorrupt) {
		t.Fatalf("process limit: got %v, want *LimitError", err)
	}

	const w, h = 30000, 30000 // progressive planes alone are over the 1 GiB cap
	big := []byte{0xff, 0xd8, 0xff, 0xc2, 0, 17, 8, h >> 8, h & 255, w >> 8, w & 255, 3, 1, 0x11, 0, 2, 0x11, 0, 3, 0x11, 0, 0xff, 0xda, 0, 12, 3, 1, 0, 2, 0, 3, 0, 0, 0, 0}
	jpeg.SetMemoryLimit(prev)
	r, err = newJPEG(context.Background(), io.NewSectionReader(bytes.NewReader(big), 0, int64(len(big))), Params{SPP: 3, W: w, H: h, BandBytes: 1 << 40})
	if err == nil {
		err = r.ReadRow(make([]byte, 3*w))
	}
	if !errors.As(err, &le) || errors.Is(err, e.ErrCorrupt) {
		t.Fatalf("1 GiB cap: got %v, want *LimitError", err)
	}

	err = jpegErr(fmt.Errorf("jpeg: %w: %w", context.Canceled, jpeg.ErrMemoryLimit), "jpeg data")
	if errors.As(err, &le) || errors.Is(err, e.ErrCorrupt) || !errors.Is(err, context.Canceled) {
		t.Fatalf("transient: got %v, want the context error", err)
	}
}
