// Package codec turns a strip's or tile's compressed bytes into packed rows,
// one row at a time, so no decoder ever holds more than a row (JPEG: a band).
package codec

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"github.com/dobbo-ca/deflate-go/zlib"
	"golang.org/x/image/ccitt"

	"github.com/dobbo-ca/tiff-go/internal/e"
	"github.com/dobbo-ca/tiff-go/internal/ifd"
)

// RowReader yields the chunk's rows in order, len(dst) bytes each.
type RowReader interface {
	ReadRow(dst []byte) error
}

// Params describes one strip or tile.
type Params struct {
	Compression, Photometric int
	T4, T6                   uint64
	SPP, BPS                 int
	W, H                     int // the chunk's frame: tile size, or strip width and rows
	JPEGTables               []byte
	BandBytes                int64
}

type stream struct{ r io.Reader }

func (s stream) ReadRow(dst []byte) error {
	_, err := io.ReadFull(s.r, dst)
	switch err {
	case nil:
		return nil
	case io.EOF, io.ErrUnexpectedEOF:
		return e.Corrupt("truncated data")
	}
	return fmt.Errorf("%w: %w", e.ErrCorrupt, err)
}

// New opens a row reader over src, the chunk's compressed bytes.
func New(ctx context.Context, src *io.SectionReader, p Params) (RowReader, error) {
	br := bufio.NewReader(src)
	switch p.Compression {
	case ifd.None:
		return stream{br}, nil
	case ifd.PackBits:
		return stream{&packbits{r: br}}, nil
	case ifd.LZW:
		d, err := newLZW(br)
		if err != nil {
			return nil, err
		}
		return stream{d}, nil
	case ifd.Deflate, ifd.DeflateX:
		z, err := zlib.NewReader(br)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", e.ErrCorrupt, err)
		}
		return stream{z}, nil
	case ifd.CCITT3, ifd.CCITT4:
		if p.SPP != 1 || p.BPS != 1 {
			return nil, e.Unsupported("ccitt with %d samples of %d bits", p.SPP, p.BPS)
		}
		// libtiff hands back the decoded bits as they are, whatever the photometric.
		sf, opts := ccitt.Group4, &ccitt.Options{Invert: true}
		if p.Compression == ifd.CCITT3 {
			if p.T4&5 != 0 { // 2-D coding, EOL fill bits: x/image/ccitt cannot read either
				return nil, e.Unsupported("group 3 options %d", p.T4)
			}
			sf = ccitt.Group3
		}
		return stream{ccitt.NewReader(br, ccitt.MSB, sf, p.W, p.H, opts)}, nil
	case ifd.JPEG:
		return newJPEG(ctx, src, p)
	}
	return nil, e.Unsupported("compression %d", p.Compression)
}
