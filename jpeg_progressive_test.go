package tiff_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dobbo-ca/jpeg-go"

	"github.com/dobbo-ca/tiff-go"
	"github.com/dobbo-ca/tiff-go/internal/fixture"
)

func progressiveTIFF(t *testing.T, name string) (tif, want []byte) {
	t.Helper()
	j, err := os.ReadFile(filepath.Join("testdata", "synthetic", name+".jpg"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(context.Background(), j, &jpeg.DecodeOptions{Format: jpeg.FormatRGBA})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := jpeg.DecodeConfig(j, nil)
	for y := 0; y < cfg.Height; y++ {
		for x := 0; x < cfg.Width; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			want = append(want, byte(r>>8), byte(g>>8), byte(b>>8))
		}
	}
	return fixture.File{Pages: []fixture.Page{{W: cfg.Width, H: cfg.Height, SPP: 3, BPS: 8, Photometric: 2, Compression: 7, RowsPerStrip: cfg.Height, Chunks: [][]byte{j}}}}.Bytes(), want
}

// A progressive strip over a band decodes by rows from one reader and equals the whole-frame decode.
func TestJPEGProgressiveStripBands(t *testing.T) {
	for _, name := range []string{"jpeg_progressive_444", "jpeg_progressive_420"} {
		b, want := progressiveTIFF(t, name)
		got, maxBand, err := decode(t, b, tiff.Limits{MaxBandBytes: 3 * 40 * 8}, 0)
		if err != nil || !bytes.Equal(got, want) || maxBand > 3*40*8 {
			t.Errorf("%s: err %v, equal %v, band %d", name, err, bytes.Equal(got, want), maxBand)
		}
	}
}

// Rows gives the row reader's budget back on every exit, so the next decode fits a limit of one frame.
func TestRowsReleasesOnEarlyExit(t *testing.T) {
	b, _ := progressiveTIFF(t, "jpeg_progressive_420")
	j, _ := os.ReadFile(filepath.Join("testdata", "synthetic", "jpeg_progressive_420.jpg"))
	need, err := jpeg.RowReaderNeed(j, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer jpeg.SetMemoryLimit(jpeg.SetMemoryLimit(need))
	f, err := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{MaxBandBytes: 3 * 40 * 8})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := f.Page(0)
	boom := errors.New("boom")
	ctx, cancel := context.WithCancel(context.Background())
	for name, run := range map[string]func() error{
		"fn error": func() error { return p.Rows(context.Background(), func(int, []byte) error { return boom }) },
		"cancel": func() error {
			return p.Rows(ctx, func(int, []byte) error { cancel(); return nil })
		},
	} {
		if err := run(); err == nil {
			t.Fatalf("%s: no error", name)
		}
		c, stop := context.WithTimeout(context.Background(), time.Second)
		err := p.Rows(c, func(int, []byte) error { return nil })
		stop()
		if err != nil {
			t.Fatalf("after %s: %v", name, err)
		}
	}
}
