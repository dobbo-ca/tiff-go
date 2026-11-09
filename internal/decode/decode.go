// Package decode assembles a page's strips or tiles into bands of rows.
package decode

import (
	"context"
	"encoding/binary"
	"io"

	"github.com/dobbo-ca/tiff-go/internal/codec"
	"github.com/dobbo-ca/tiff-go/internal/e"
	"github.com/dobbo-ca/tiff-go/internal/ifd"
)

func params(p *ifd.Page, w, h int, bandBytes int64) codec.Params {
	return codec.Params{Compression: p.Compression, Photometric: p.Photometric, T4: p.T4, T6: p.T6,
		SPP: p.SPP, BPS: p.BPS, W: w, H: h, JPEGTables: p.JPEGTables, BandBytes: bandBytes}
}

// fix undoes the predictor and puts 16-bit samples in big-endian order.
// JPEG and CCITT output needs neither.
func fix(p *ifd.Page, row []byte, widthPx int) {
	if p.Compression == ifd.JPEG || p.Compression == ifd.CCITT3 || p.Compression == ifd.CCITT4 {
		return
	}
	bo := p.F.ByteOrder()
	n := widthPx * p.SPP
	switch {
	case p.Predictor == 2 && p.BPS == 8:
		for i := p.SPP; i < n; i++ {
			row[i] += row[i-p.SPP]
		}
	case p.Predictor == 2 && p.BPS == 16:
		for i := p.SPP; i < n; i++ {
			bo.PutUint16(row[2*i:], bo.Uint16(row[2*i:])+bo.Uint16(row[2*(i-p.SPP):]))
		}
	}
	if p.BPS == 16 && bo == binary.LittleEndian {
		for i := 0; i < n; i++ {
			row[2*i], row[2*i+1] = row[2*i+1], row[2*i]
		}
	}
}

// Rows calls fn with consecutive bands of full-width rows, top to bottom.
// No band exceeds bandBytes. fn must not keep the slice.
func Rows(ctx context.Context, p *ifd.Page, bandBytes int64, fn func(y int, rows []byte) error) error {
	rb := p.RowBytes()
	if rb > bandBytes {
		return &e.LimitError{Name: "band bytes", Got: rb, Max: bandBytes}
	}
	if p.TileW > 0 {
		return tiles(ctx, p, rb, bandBytes, fn)
	}
	buf := make([]byte, min(int64(p.RowsPerStrip), bandBytes/rb)*rb)
	for s := 0; s < p.Chunks(); s++ {
		y0 := s * p.RowsPerStrip
		rows := min(p.RowsPerStrip, p.Height-y0)
		off, n, err := p.F.Chunk(p.Offsets, p.Counts, uint64(s))
		if err != nil {
			return err
		}
		err = func() error {
			rr, err := codec.New(ctx, p.F.Section(off, n), params(p, p.Width, rows, bandBytes))
			if err != nil {
				return err
			}
			if c, ok := rr.(io.Closer); ok {
				defer c.Close() // a jpeg-go reader holds budget until closed
			}
			for done := 0; done < rows; {
				if err := ctx.Err(); err != nil {
					return err
				}
				k := min(int64(rows-done), int64(len(buf))/rb)
				for r := int64(0); r < k; r++ {
					row := buf[r*rb : (r+1)*rb]
					if err := rr.ReadRow(row); err != nil {
						return err
					}
					fix(p, row, p.Width)
				}
				if err := fn(y0+done, buf[:k*rb]); err != nil {
					return err
				}
				done += int(k)
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// tiles decodes one tile row at a time: every tile of the row, row by row,
// into a buffer of the tile row's full-width rows. A tile row bigger than
// bandBytes is refused (docs/decisions.md).
func tiles(ctx context.Context, p *ifd.Page, rb, bandBytes int64, fn func(y int, rows []byte) error) error {
	trb := (int64(p.TileW)*int64(p.SPP)*int64(p.BPS) + 7) / 8
	if got := int64(min(p.TileL, p.Height)) * rb; got > bandBytes {
		return &e.LimitError{Name: "band bytes", Got: got, Max: bandBytes}
	}
	if trb > bandBytes {
		return &e.LimitError{Name: "band bytes", Got: trb, Max: bandBytes}
	}
	buf := make([]byte, int64(min(p.TileL, p.Height))*rb)
	tmp := make([]byte, trb)
	for ty := 0; ty < p.Chunks()/p.TilesAcross(); ty++ {
		y0 := ty * p.TileL
		rows := min(p.TileL, p.Height-y0)
		for tx := 0; tx < p.TilesAcross(); tx++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			off, n, err := p.F.Chunk(p.Offsets, p.Counts, uint64(ty*p.TilesAcross()+tx))
			if err != nil {
				return err
			}
			err = func() error {
				rr, err := codec.New(ctx, p.F.Section(off, n), params(p, p.TileW, p.TileL, bandBytes))
				if err != nil {
					return err
				}
				if c, ok := rr.(io.Closer); ok {
					defer c.Close()
				}
				xoff := int64(tx) * trb // tile widths are byte-aligned (checked at parse)
				for r := 0; r < rows; r++ {
					if err := rr.ReadRow(tmp); err != nil {
						return err
					}
					fix(p, tmp, p.TileW)
					copy(buf[int64(r)*rb+xoff:int64(r+1)*rb], tmp)
				}
				return nil
			}()
			if err != nil {
				return err
			}
		}
		if err := fn(y0, buf[:int64(rows)*rb]); err != nil {
			return err
		}
	}
	return nil
}
