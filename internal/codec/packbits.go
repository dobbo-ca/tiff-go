package codec

import (
	"bufio"
	"io"
)

// packbits streams the Apple PackBits run-length scheme.
type packbits struct {
	r   *bufio.Reader
	lit int  // literal bytes left in the current packet
	rep int  // repeats left in the current run
	b   byte // the repeated byte
}

func (d *packbits) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		switch {
		case d.lit > 0:
			m, err := io.ReadFull(d.r, p[n:n+min(d.lit, len(p)-n)])
			n, d.lit = n+m, d.lit-m
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return n, err
			}
		case d.rep > 0:
			m := min(d.rep, len(p)-n)
			for i := range m {
				p[n+i] = d.b
			}
			n, d.rep = n+m, d.rep-m
		default:
			c, err := d.r.ReadByte()
			if err == nil && c < 128 {
				d.lit = int(c) + 1
			} else if err == nil && c > 128 {
				d.rep = 257 - int(c)
				d.b, err = d.r.ReadByte()
			}
			if err != nil {
				if n > 0 {
					return n, nil
				}
				return 0, err
			}
		}
	}
	return n, nil
}
