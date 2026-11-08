package tiff

import (
	"context"
	"io"

	"github.com/dobbo-ca/tiff-go/internal/decode"
	"github.com/dobbo-ca/tiff-go/internal/e"
	"github.com/dobbo-ca/tiff-go/internal/ifd"
)

// Errors every failure wraps. Messages carry numbers and tag ids only, never
// file content.
var (
	ErrLimit       = e.ErrLimit
	ErrCorrupt     = e.ErrCorrupt
	ErrUnsupported = e.ErrUnsupported
)

// LimitError names the cap that tripped; it wraps ErrLimit.
type LimitError = e.LimitError

// Defaults applied to zero Limits fields.
const (
	DefaultMaxIFDs      = 10000
	DefaultMaxPixels    = 178956970 // kleio's upload ceiling, per IFD
	DefaultMaxBandBytes = 64 << 20
)

// Limits caps what one File may cost. The zero value is safe: every zero
// field takes its default.
type Limits struct {
	MaxIFDs      int   // directories in the chain
	MaxPixels    int64 // Width*Height of one IFD
	MaxBandBytes int64 // decoded bytes buffered at once
}

func (l Limits) withDefaults() Limits {
	if l.MaxIFDs <= 0 {
		l.MaxIFDs = DefaultMaxIFDs
	}
	if l.MaxPixels <= 0 {
		l.MaxPixels = DefaultMaxPixels
	}
	if l.MaxBandBytes <= 0 {
		l.MaxBandBytes = DefaultMaxBandBytes
	}
	return l
}

// Photometric values of Info.Photometric.
const (
	WhiteIsZero = ifd.WhiteIsZero
	BlackIsZero = ifd.BlackIsZero
	RGB         = ifd.RGB
	Palette     = ifd.Palette
	CMYK        = ifd.CMYK
)

// File is an opened TIFF.
type File struct {
	f   *ifd.File
	lim Limits
}

// Open reads the header and the IFD chain of the size bytes behind ra.
func Open(ra io.ReaderAt, size int64, lim Limits) (*File, error) {
	lim = lim.withDefaults()
	f, err := ifd.Open(ra, size, lim.MaxIFDs)
	if err != nil {
		return nil, err
	}
	return &File{f: f, lim: lim}, nil
}

// NumPages reports the number of IFDs.
func (f *File) NumPages() int { return len(f.f.Offs) }

// Page parses IFD i.
func (f *File) Page(i int) (*Page, error) {
	if i < 0 || i >= f.NumPages() {
		return nil, e.Corrupt("page %d out of range", i)
	}
	p, err := f.f.Page(i, f.lim.MaxPixels)
	if err != nil {
		return nil, err
	}
	return &Page{p: p, lim: f.lim}, nil
}

// Info describes a page's samples.
type Info struct {
	Width, Height   int
	SamplesPerPixel int
	BitsPerSample   int
	Photometric     int      // as stored; JPEG-compressed YCbCr reports RGB
	Compression     int      // TIFF compression code
	Orientation     int      // TIFF Orientation, 1 when absent
	RowBytes        int64    // bytes in one delivered row
	ColorMap        []uint16 // 3<<BitsPerSample entries for Palette: reds, greens, blues
	ExtraSamples    []uint16
}

// Page is one parsed directory.
type Page struct {
	p   *ifd.Page
	lim Limits
}

// Info returns the page's description.
func (p *Page) Info() Info {
	ph := p.p.Photometric
	if ph == ifd.YCbCr {
		ph = RGB
	}
	return Info{Width: p.p.Width, Height: p.p.Height, SamplesPerPixel: p.p.SPP, BitsPerSample: p.p.BPS,
		Photometric: ph, Compression: p.p.Compression, Orientation: p.p.Orientation, RowBytes: p.p.RowBytes(),
		ColorMap: p.p.ColorMap, ExtraSamples: p.p.ExtraSamples}
}

// Rows calls fn with consecutive bands of full-width rows starting at row y.
// A band is whole rows and at most MaxBandBytes; fn must not keep it. ctx is
// checked before every band, and an error from fn stops the decode.
func (p *Page) Rows(ctx context.Context, fn func(y int, rows []byte) error) error {
	return decode.Rows(ctx, p.p, p.lim.MaxBandBytes, fn)
}
