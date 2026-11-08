package codec

import (
	"bufio"
	"io"

	"github.com/dobbo-ca/tiff-go/internal/e"
)

// lzw is the TIFF 6.0 LZW decoder: MSB-first codes of 9 to 12 bits, clear
// code 256, end code 257, and the "early change" width switch.
type lzw struct {
	r      *bufio.Reader
	acc    uint32
	nbits  uint
	width  uint
	next   int
	prev   int // -1 after a clear code
	prefix [4096]uint16
	suffix [4096]byte
	buf    [4097]byte
	pend   []byte
	done   bool
}

func newLZW(r *bufio.Reader) (*lzw, error) {
	if h, err := r.Peek(2); err == nil && h[0] == 0 && h[1]&1 != 0 {
		return nil, e.Unsupported("old-style lzw")
	}
	return &lzw{r: r, width: 9, next: 258, prev: -1}, nil
}

func (d *lzw) code() (int, error) {
	for d.nbits < d.width {
		b, err := d.r.ReadByte()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		d.acc = d.acc<<8 | uint32(b)
		d.nbits += 8
	}
	d.nbits -= d.width
	return int(d.acc>>d.nbits) & (1<<d.width - 1), nil
}

func (d *lzw) Read(p []byte) (int, error) {
	for len(d.pend) == 0 {
		if d.done {
			return 0, io.EOF
		}
		if err := d.step(); err != nil {
			return 0, err
		}
	}
	n := copy(p, d.pend)
	d.pend = d.pend[n:]
	return n, nil
}

// step decodes one code into d.pend.
func (d *lzw) step() error {
	c, err := d.code()
	if err != nil {
		return err
	}
	switch c {
	case 256:
		d.width, d.next, d.prev = 9, 258, -1
		return nil
	case 257:
		d.done = true
		return nil
	}
	end := len(d.buf)
	var start int
	switch {
	case c < d.next:
		start = d.expand(c, end)
	case c == d.next && d.prev >= 0: // KwKwK: previous string plus its own first byte
		end--
		start = d.expand(d.prev, end)
		d.buf[end] = d.buf[start]
		end++
	default:
		return e.Corrupt("lzw code")
	}
	if d.prev >= 0 && d.next < 4096 {
		d.prefix[d.next], d.suffix[d.next] = uint16(d.prev), d.buf[start]
		d.next++
		if d.next >= 1<<d.width-1 && d.width < 12 {
			d.width++
		}
	}
	d.prev, d.pend = c, d.buf[start:end]
	return nil
}

// expand writes the string for code c backwards, ending at end, and
// returns its start.
func (d *lzw) expand(c, end int) int {
	i := end
	for c >= 258 {
		i--
		d.buf[i] = d.suffix[c]
		c = int(d.prefix[c])
	}
	i--
	d.buf[i] = byte(c)
	return i
}
