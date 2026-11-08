package codec

import (
	"context"
	"image"
	"io"

	"github.com/dobbo-ca/jpeg-go"

	"github.com/dobbo-ca/tiff-go/internal/e"
)

// jpegRows decodes a strip or tile a band of rows at a time through the
// jpeg-go region decoder. The compressed chunk is held whole (its API takes
// a slice); decoded pixels are never held beyond one band.
type jpegRows struct {
	ctx      context.Context
	src      []byte
	opts     jpeg.DecodeOptions
	w, h     int
	bandRows int
	next     int // next row to hand out
	band     image.Image
	bandEnd  int
}

func newJPEG(ctx context.Context, src *io.SectionReader, p Params) (RowReader, error) {
	if p.SPP != 1 && p.SPP != 3 {
		return nil, e.Unsupported("jpeg with %d samples", p.SPP)
	}
	b := make([]byte, src.Size())
	if _, err := src.ReadAt(b, 0); err != nil && err != io.EOF {
		return nil, err
	}
	cfg, err := jpeg.DecodeConfig(b, p.JPEGTables)
	if err != nil {
		return nil, e.Corrupt("jpeg header")
	}
	if cfg.Width != p.W || cfg.Height != p.H || cfg.Components != p.SPP {
		return nil, e.Corrupt("jpeg frame does not match its chunk")
	}
	j := &jpegRows{ctx: ctx, src: b, w: p.W, h: p.H}
	j.opts = jpeg.DecodeOptions{Format: jpeg.FormatRGBA, Tables: p.JPEGTables}
	px := int64(4) // decoded bytes per pixel
	if p.SPP == 1 {
		j.opts.Format, px = jpeg.FormatGray, 1
	}
	if px*int64(p.W) > p.BandBytes {
		return nil, &e.LimitError{Name: "band bytes", Got: px * int64(p.W), Max: p.BandBytes}
	}
	j.bandRows = int(min(int64(p.H), p.BandBytes/(px*int64(p.W))))
	if j.bandRows < j.h && !bandable(b) { // jpeg-go decodes the whole frame per band
		return nil, e.Unsupported("jpeg larger than a band that is not baseline interleaved")
	}
	return j, nil
}

func (j *jpegRows) ReadRow(dst []byte) error {
	if j.next >= j.h {
		return e.Corrupt("jpeg rows")
	}
	if j.next >= j.bandEnd {
		if err := j.ctx.Err(); err != nil {
			return err
		}
		end := min(j.next+j.bandRows, j.h)
		j.opts.Region = image.Rectangle{}
		if j.bandRows < j.h {
			j.opts.Region = image.Rect(0, j.next, j.w, end)
		}
		img, err := jpeg.Decode(j.ctx, j.src, &j.opts)
		if err != nil {
			return e.Corrupt("jpeg data")
		}
		j.band, j.bandEnd = img, end
	}
	switch m := j.band.(type) {
	case *image.RGBA:
		s := m.Pix[m.PixOffset(m.Rect.Min.X, j.next):]
		for i := 0; i < j.w; i++ {
			dst[3*i], dst[3*i+1], dst[3*i+2] = s[4*i], s[4*i+1], s[4*i+2]
		}
	case *image.Gray:
		copy(dst, m.Pix[m.PixOffset(m.Rect.Min.X, j.next):][:j.w])
	default:
		return e.Corrupt("jpeg output")
	}
	j.next++
	return nil
}

// bandable reports whether jpeg-go can decode b a band at a time: a baseline
// frame that is single-component or whose first scan carries every component
// (the same gate as jpeg-go; otherwise each band re-decodes the whole frame).
// Markers are found as jpeg-go's findMarker does: bytes between segments are
// skipped, and TEM and RSTn have no length. Any other reading would let a file
// show this gate one frame and the decoder another.
func bandable(b []byte) bool {
	n := 0 // frame components, 0 until SOF is seen
	for i := 2; ; {
		var m byte
		for m == 0 {
			for i < len(b) && b[i] != 0xff {
				i++
			}
			for i < len(b) && b[i] == 0xff {
				i++
			}
			if i >= len(b) {
				return false
			}
			m = b[i]
			i++
		}
		if m == 0x01 || m >= 0xd0 && m <= 0xd7 {
			continue
		}
		if m == 0xd8 || m == 0xd9 || i+2 > len(b) {
			return false // duplicate SOI, EOI, truncated
		}
		l := int(b[i])<<8 | int(b[i+1])
		if l < 2 && (m >= 0xe0 && m <= 0xef || m == 0xfe || m == 0xdc) {
			l = 2
		}
		if l < 2 || i+l > len(b) && m != 0xda { // jpeg-go reads a short SOS as is
			return false
		}
		switch {
		case m == 0xc0 || m == 0xc1: // baseline / extended sequential
			if l < 8 {
				return false
			}
			n = int(b[i+7])
			if n == 0 {
				return false
			}
		case m >= 0xc2 && m <= 0xcf && m != 0xc4 && m != 0xc8 && m != 0xcc:
			return false // progressive, lossless, arithmetic
		case m == 0xda: // first scan
			return n == 1 || n > 0 && i+2 < len(b) && int(b[i+2]) == n
		}
		i += l
	}
}
