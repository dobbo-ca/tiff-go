package tiff_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	tiff "github.com/dobbo-ca/tiff-go"
	"github.com/dobbo-ca/tiff-go/internal/fixture"
)

// decode reassembles page i and reports the largest band handed out.
func decode(t testing.TB, b []byte, lim tiff.Limits, i int) (pix []byte, maxBand int, err error) {
	t.Helper()
	f, err := tiff.Open(bytes.NewReader(b), int64(len(b)), lim)
	if err != nil {
		return nil, 0, err
	}
	p, err := f.Page(i)
	if err != nil {
		return nil, 0, err
	}
	next := 0
	err = p.Rows(context.Background(), func(y int, rows []byte) error {
		if y != next {
			t.Fatalf("band starts at row %d, want %d", y, next)
		}
		maxBand = max(maxBand, len(rows))
		pix = append(pix, rows...)
		next += len(rows) / int(p.Info().RowBytes)
		return nil
	})
	return
}

func photometric(spp int) int {
	return map[int]int{1: 1, 3: 2, 4: 5}[spp]
}

func TestRoundTrip(t *testing.T) {
	formats := []struct{ spp, bps int }{{1, 1}, {1, 4}, {1, 8}, {3, 8}, {4, 8}, {1, 16}, {3, 16}}
	for _, comp := range []int{1, 5, 8, 32946, 32773} {
		for _, f := range formats {
			for _, pred := range []int{0, 2} {
				if pred == 2 && f.bps < 8 {
					continue
				}
				for _, lay := range []string{"strip1", "strip7", "whole", "tile"} {
					for _, le := range []bool{false, true} {
						for _, big := range []bool{false, true} {
							if big && (comp != 1 || lay != "whole" || f.spp != 3) && (!le || f.bps != 16) {
								continue // BigTIFF: a few shapes are enough
							}
							name := fmt.Sprintf("c%d/%dx%d/p%d/%s/le=%v/big=%v", comp, f.spp, f.bps, pred, lay, le, big)
							t.Run(name, func(t *testing.T) {
								p := fixture.Page{W: 37, H: 50, SPP: f.spp, BPS: f.bps, Photometric: photometric(f.spp),
									Compression: comp, Predictor: pred}
								switch lay {
								case "strip1":
									p.RowsPerStrip = 1
								case "strip7":
									p.RowsPerStrip = 7
								case "tile":
									p.TileW, p.TileL = 16, 16
								}
								p.Pix = fixture.Pattern(p.W, p.H, p.SPP, p.BPS)
								b := fixture.File{LittleEndian: le, Big: big, Pages: []fixture.Page{p}}.Bytes()
								got, _, err := decode(t, b, tiff.Limits{}, 0)
								if err != nil {
									t.Fatal(err)
								}
								if !bytes.Equal(got, p.Pix) {
									t.Fatal("pixels differ")
								}
							})
						}
					}
				}
			}
		}
	}
}

func TestMultiPage(t *testing.T) {
	var pages []fixture.Page
	for i := 0; i < 3; i++ {
		pages = append(pages, fixture.Page{W: 10 + i, H: 5 + 2*i, SPP: 1, BPS: 8, Photometric: 1, Compression: 5 * (i % 2)})
		pages[i].Pix = fixture.Pattern(pages[i].W, pages[i].H, 1, 8)
	}
	b := fixture.File{Pages: pages}.Bytes()
	f, err := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
	if err != nil || f.NumPages() != 3 {
		t.Fatalf("open: %v, pages %d", err, f.NumPages())
	}
	for i, p := range pages {
		got, _, err := decode(t, b, tiff.Limits{}, i)
		if err != nil || !bytes.Equal(got, p.Pix) {
			t.Fatalf("page %d: %v", i, err)
		}
	}
	if _, err := f.Page(3); err == nil {
		t.Fatal("page 3 should not exist")
	}
}

// A single strip far over the band cap decodes in bands, allocating a small
// fraction of its decoded size (e2e-tiff-strip-rgb and -jpeg, scaled down).
func TestSingleStripStreams(t *testing.T) {
	const w, h, cap = 2000, 1000, 64 << 10
	for _, comp := range []int{1, 5, 8, 32773, 7} {
		t.Run(fmt.Sprint(comp), func(t *testing.T) {
			p := fixture.Page{W: w, H: h, SPP: 3, BPS: 8, Photometric: 2, Compression: comp}
			p.Pix = fixture.Pattern(w, h, 3, 8)
			b := fixture.File{Pages: []fixture.Page{p}}.Bytes()
			f, err := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{MaxBandBytes: cap})
			if err != nil {
				t.Fatal(err)
			}
			pg, err := f.Page(0)
			if err != nil {
				t.Fatal(err)
			}
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m0)
			maxBand := 0
			err = pg.Rows(context.Background(), func(y int, rows []byte) error {
				maxBand = max(maxBand, len(rows))
				want := p.Pix[y*w*3 : y*w*3+len(rows)]
				if comp == 7 {
					assertClose(t, rows, want, 24)
				} else if !bytes.Equal(rows, want) {
					t.Fatalf("pixels differ at row %d", y)
				}
				return nil
			})
			runtime.ReadMemStats(&m1)
			if err != nil {
				t.Fatal(err)
			}
			if maxBand > cap {
				t.Fatalf("band %d > cap %d", maxBand, cap)
			}
			// the image decodes to 6 MB; the decoder must stay far below that.
			// JPEG is exempt: jpeg-go returns a fresh image per Region call, so its
			// total is churn (one band each), not live memory; the band cap above bounds it.
			if d := m1.TotalAlloc - m0.TotalAlloc; d > 3<<20 && comp != 7 {
				t.Fatalf("decoder allocated %d bytes", d)
			}
		})
	}
}

func assertClose(t *testing.T, got, want []byte, tol int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length %d, want %d", len(got), len(want))
	}
	for i := range got {
		if d := int(got[i]) - int(want[i]); d > tol || d < -tol {
			t.Fatalf("byte %d: %d vs %d", i, got[i], want[i])
		}
	}
}

func TestJPEG(t *testing.T) {
	for _, lay := range []string{"strips", "tiles"} {
		for _, spp := range []int{1, 3} {
			t.Run(fmt.Sprint(lay, spp), func(t *testing.T) {
				p := fixture.Page{W: 70, H: 50, SPP: spp, BPS: 8, Photometric: photometric(spp), Compression: 7, RowsPerStrip: 20}
				if lay == "tiles" {
					p.RowsPerStrip, p.TileW, p.TileL = 0, 32, 32
				}
				// smooth content so lossy error is small
				p.Pix = make([]byte, p.W*spp*p.H)
				for i := range p.Pix {
					p.Pix[i] = byte(i%(p.W*spp)/spp*2 + i/(p.W*spp))
				}
				b := fixture.File{Pages: []fixture.Page{p}}.Bytes()
				whole, _, err := decode(t, b, tiff.Limits{}, 0)
				if err != nil {
					t.Fatal(err)
				}
				assertClose(t, whole, p.Pix, 12)
				banded, _, err := decode(t, b, tiff.Limits{MaxBandBytes: int64(p.W*spp*8) * 4}, 0)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(banded, whole) {
					t.Fatal("banded decode differs from whole-chunk decode")
				}
			})
		}
	}
}

func TestInfoPalette(t *testing.T) {
	cm := make([]uint16, 3*16)
	for i := range cm {
		cm[i] = uint16(i * 1000)
	}
	p := fixture.Page{W: 8, H: 8, SPP: 1, BPS: 4, Photometric: 3, ColorMap: cm}
	b := fixture.File{LittleEndian: true, Pages: []fixture.Page{p}}.Bytes()
	f, _ := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
	pg, err := f.Page(0)
	if err != nil {
		t.Fatal(err)
	}
	in := pg.Info()
	if in.Width != 8 || in.Height != 8 || in.BitsPerSample != 4 || in.Photometric != tiff.Palette || len(in.ColorMap) != 48 || in.ColorMap[47] != 47000 {
		t.Fatalf("info %+v", in)
	}
}

// Group 4: an all-white row (V0), an all-black row (horizontal mode), the same
// row again (V0 V0), then EOFB.
func TestCCITT(t *testing.T) {
	bits := "1" + "001" + "00110101" + "000101" + "11" + "000000000001000000000001"
	var data []byte
	for len(bits)%8 != 0 {
		bits += "0"
	}
	for i := 0; i < len(bits); i += 8 {
		var v byte
		for _, c := range bits[i : i+8] {
			v = v<<1 | byte(c-'0')
		}
		data = append(data, v)
	}
	// libtiff returns the decoded bits as they are, whatever the photometric.
	for photo, want := range map[int][]byte{1: {0x00, 0xff, 0xff}, 0: {0x00, 0xff, 0xff}} {
		p := fixture.Page{W: 8, H: 3, SPP: 1, BPS: 1, Photometric: photo, Compression: 4, Chunks: [][]byte{data}}
		b := fixture.File{Pages: []fixture.Page{p}}.Bytes()
		got, _, err := decode(t, b, tiff.Limits{}, 0)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("photometric %d: %x %v, want %x", photo, got, err, want)
		}
	}
}

func TestLimits(t *testing.T) {
	page := func(w, h int) fixture.Page { return fixture.Page{W: w, H: h, SPP: 1, BPS: 8, Photometric: 1} }
	open := func(b []byte, lim tiff.Limits) error {
		_, err := tiff.Open(bytes.NewReader(b), int64(len(b)), lim)
		return err
	}
	many := func(n int) []byte {
		pp := make([]fixture.Page, n)
		for i := range pp {
			pp[i] = page(1, 1)
		}
		return fixture.File{Pages: pp}.Bytes()
	}
	t.Run("ifd count", func(t *testing.T) {
		if err := open(many(10000), tiff.Limits{}); err != nil {
			t.Fatalf("10000 IFDs: %v", err)
		}
		var le *tiff.LimitError
		err := open(many(10001), tiff.Limits{})
		if !errors.As(err, &le) || !errors.Is(err, tiff.ErrLimit) || le.Name != "ifds" {
			t.Fatalf("10001 IFDs: %v", err)
		}
	})
	t.Run("ifd loop", func(t *testing.T) {
		b := fixture.File{Pages: []fixture.Page{page(1, 1), page(1, 1)}}.Bytes()
		copy(b[len(b)-4:], b[4:8]) // last IFD's next = first IFD
		if err := open(b, tiff.Limits{}); !errors.Is(err, tiff.ErrCorrupt) {
			t.Fatalf("loop: %v", err)
		}
	})
	t.Run("pixels", func(t *testing.T) {
		b := fixture.File{Pages: []fixture.Page{page(1000, 1000)}}.Bytes()
		f, _ := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{MaxPixels: 999999})
		var le *tiff.LimitError
		if _, err := f.Page(0); !errors.As(err, &le) || le.Name != "pixels" {
			t.Fatalf("pixels: %v", err)
		}
	})
	t.Run("defaults refuse a huge header", func(t *testing.T) {
		p := page(1<<31, 1<<31)
		p.Chunks, p.Pix = [][]byte{{0}}, []byte{0}
		b := fixture.File{Pages: []fixture.Page{p}}.Bytes()
		f, err := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Page(0); !errors.Is(err, tiff.ErrLimit) {
			t.Fatalf("huge: %v", err)
		}
	})
	t.Run("row wider than the band", func(t *testing.T) {
		b := fixture.File{Pages: []fixture.Page{page(100, 4)}}.Bytes()
		_, _, err := decode(t, b, tiff.Limits{MaxBandBytes: 99}, 0)
		if !errors.Is(err, tiff.ErrLimit) {
			t.Fatalf("band: %v", err)
		}
	})
	t.Run("tile row wider than the band", func(t *testing.T) {
		p := page(64, 64)
		p.TileW, p.TileL = 16, 16
		b := fixture.File{Pages: []fixture.Page{p}}.Bytes()
		_, _, err := decode(t, b, tiff.Limits{MaxBandBytes: 64*16 - 1}, 0)
		if !errors.Is(err, tiff.ErrLimit) {
			t.Fatalf("tile band: %v", err)
		}
	})
}

func TestRefusals(t *testing.T) {
	base := fixture.Page{W: 8, H: 8, SPP: 1, BPS: 8, Photometric: 1}
	for name, mut := range map[string]func(*fixture.Page){
		"lab":          func(p *fixture.Page) { p.Photometric = 8 },
		"old jpeg":     func(p *fixture.Page) { p.Compression = 6 },
		"float pred":   func(p *fixture.Page) { p.Predictor = 3 },
		"pred at 4bit": func(p *fixture.Page) { p.Predictor, p.BPS = 2, 4 },
	} {
		t.Run(name, func(t *testing.T) {
			p := base
			mut(&p)
			p.Chunks = [][]byte{make([]byte, 64)}
			b := fixture.File{Pages: []fixture.Page{p}}.Bytes()
			if _, _, err := decode(t, b, tiff.Limits{}, 0); !errors.Is(err, tiff.ErrUnsupported) {
				t.Fatalf("%v", err)
			}
		})
	}
	t.Run("truncated", func(t *testing.T) {
		for _, comp := range []int{1, 5, 8, 32773} {
			p := base
			p.Compression = comp
			p.Chunks = [][]byte{{1, 2}}
			b := fixture.File{Pages: []fixture.Page{p}}.Bytes()
			if _, _, err := decode(t, b, tiff.Limits{}, 0); !errors.Is(err, tiff.ErrCorrupt) {
				t.Fatalf("comp %d: %v", comp, err)
			}
		}
	})
	t.Run("not a tiff", func(t *testing.T) {
		if _, err := tiff.Open(bytes.NewReader([]byte("GIF89a..")), 8, tiff.Limits{}); !errors.Is(err, tiff.ErrCorrupt) {
			t.Fatalf("%v", err)
		}
	})
}

func TestContext(t *testing.T) {
	p := fixture.Page{W: 100, H: 100, SPP: 1, BPS: 8, Photometric: 1, RowsPerStrip: 10}
	b := fixture.File{Pages: []fixture.Page{p}}.Bytes()
	f, _ := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
	pg, _ := f.Page(0)
	ctx, cancel := context.WithCancel(context.Background())
	bands := 0
	err := pg.Rows(ctx, func(int, []byte) error {
		bands++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || bands != 1 {
		t.Fatalf("bands %d, err %v", bands, err)
	}
}

func FuzzTIFF(f *testing.F) {
	for _, comp := range []int{1, 5, 7, 8, 32773} {
		for _, tiles := range []bool{false, true} {
			p := fixture.Page{W: 33, H: 21, SPP: 3, BPS: 8, Photometric: 2, Compression: comp, RowsPerStrip: 5}
			if tiles {
				p.RowsPerStrip, p.TileW, p.TileL = 0, 16, 16
			}
			f.Add(fixture.File{Pages: []fixture.Page{p, p}}.Bytes())
		}
	}
	lim := tiff.Limits{MaxIFDs: 64, MaxPixels: 1 << 22, MaxBandBytes: 1 << 20}
	f.Fuzz(func(t *testing.T, b []byte) {
		t0 := time.Now()
		defer func() {
			if time.Since(t0) > 1500*time.Millisecond {
				t.Fatalf("slow input %v", time.Since(t0))
			}
		}()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		tf, err := tiff.Open(bytes.NewReader(b), int64(len(b)), lim)
		if err == nil {
			for i := 0; i < tf.NumPages() && i < 8; i++ {
				if p, err := tf.Page(i); err == nil {
					_ = p.Rows(context.Background(), func(int, []byte) error { return nil })
				}
			}
		}
		runtime.ReadMemStats(&m1)
		if d := m1.TotalAlloc - m0.TotalAlloc; d > 96<<20 {
			t.Fatalf("allocated %d bytes under tight limits", d)
		}
	})
}
