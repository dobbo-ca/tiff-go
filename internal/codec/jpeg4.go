package codec

import (
	"context"
	"io"

	"github.com/dobbo-ca/tiff-go/internal/e"
)

// jpeg4Rows decodes a four-component baseline JPEG strip or tile, which jpeg-go
// does not (it stops at three components). libtiff hands such a chunk to libjpeg
// with no colour space (JCS_UNKNOWN): the four samples come back as stored, an
// Adobe marker's transform ignored, and it refuses any sampling but 1x1. This
// reads a single interleaved baseline scan the same way: Huffman, dequantise,
// libjpeg's accurate integer IDCT (jidctint.c), 8 rows of MCUs at a time.
type jpeg4Rows struct {
	ctx     context.Context
	b       []byte // entropy-coded data after SOS
	pos     int
	acc     uint32 // bit buffer, n valid bits at the top
	n       uint
	w, h    int
	qt      [4][64]int32 // natural order
	dc, ac  [4]*huff
	comp    [4]struct{ q, td, ta byte }
	pred    [4]int32
	restart int // MCUs between restarts, 0 for none
	left    int // MCUs to the next restart
	mx, my  int // MCU columns, rows
	mcuRow  int // next MCU row to decode
	band    []byte
	next    int // next row (of the chunk) to hand out
}

// huff is a canonical Huffman table (ITU T.81 F.2.2.3).
type huff struct {
	maxcode [18]int32
	valptr  [17]int
	mincode [17]int32
	vals    []byte
}

var zigzag = [64]byte{
	0, 1, 8, 16, 9, 2, 3, 10, 17, 24, 32, 25, 18, 11, 4, 5, 12, 19, 26, 33, 40, 48, 41, 34, 27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36, 29, 22, 15, 23, 30, 37, 44, 51, 58, 59, 52, 45, 38, 31, 39, 46, 53, 60, 61, 54, 47, 55, 62, 63,
}

func newJPEG4(ctx context.Context, src *io.SectionReader, p Params) (RowReader, error) {
	if p.W > 65500 || p.H > 65500 {
		return nil, e.Unsupported("jpeg frame %dx%d", p.W, p.H)
	}
	mx := (p.W + 7) / 8
	if bb := int64(mx) * 8 * 8 * 4; bb > p.BandBytes {
		return nil, &e.LimitError{Name: "band bytes", Got: bb, Max: p.BandBytes}
	}
	b := make([]byte, src.Size())
	if _, err := src.ReadAt(b, 0); err != nil && err != io.EOF {
		return nil, err
	}
	j := &jpeg4Rows{ctx: ctx, w: p.W, h: p.H, mx: mx, my: (p.H + 7) / 8, band: make([]byte, mx*8*8*4)}
	var sos bool
	var err error
	for _, s := range [][]byte{p.JPEGTables, b} {
		if len(s) == 0 {
			continue
		}
		if sos, err = j.parse(s); err != nil {
			return nil, err
		}
	}
	if !sos {
		return nil, e.Corrupt("jpeg header")
	}
	return j, nil
}

// parse reads the segments of s up to SOS (setting j.b) or EOI; sos reports
// whether it reached a scan.
func (j *jpeg4Rows) parse(s []byte) (sos bool, err error) {
	if len(s) < 2 || s[0] != 0xff || s[1] != 0xd8 {
		return false, e.Corrupt("jpeg header")
	}
	for i := 2; ; {
		if i+2 > len(s) || s[i] != 0xff {
			return false, e.Corrupt("jpeg segment")
		}
		m := s[i+1]
		if m == 0xff {
			i++ // fill byte
			continue
		}
		if m == 0xd9 {
			return false, nil
		}
		if i+4 > len(s) {
			return false, e.Corrupt("jpeg segment")
		}
		l := int(s[i+2])<<8 | int(s[i+3])
		if l < 2 || i+2+l > len(s) {
			return false, e.Corrupt("jpeg segment")
		}
		seg := s[i+4 : i+2+l]
		switch {
		case m == 0xdb:
			err = j.dqt(seg)
		case m == 0xc4:
			err = j.dht(seg)
		case m == 0xc0 || m == 0xc1:
			err = j.sof(seg)
		case m >= 0xc2 && m <= 0xcf && m != 0xc4 && m != 0xc8 && m != 0xcc:
			err = e.Unsupported("jpeg that is not baseline")
		case m == 0xdd:
			if len(seg) != 2 {
				return false, e.Corrupt("jpeg restart interval")
			}
			j.restart = int(seg[0])<<8 | int(seg[1])
		case m == 0xda:
			if err = j.sos(seg); err != nil {
				return false, err
			}
			j.b = s[i+2+l:]
			return true, nil
		}
		if err != nil {
			return false, err
		}
		i += 2 + l
	}
}

func (j *jpeg4Rows) dqt(seg []byte) error {
	for len(seg) > 0 {
		pq, id := seg[0]>>4, seg[0]&15
		n := 64 * (1 + int(pq))
		if pq > 1 || id > 3 || len(seg) < 1+n {
			return e.Corrupt("jpeg quantisation table")
		}
		for k := range 64 {
			v := int32(seg[1+k])
			if pq == 1 {
				v = int32(seg[1+2*k])<<8 | int32(seg[2+2*k])
			}
			j.qt[id][zigzag[k]] = v
		}
		seg = seg[1+n:]
	}
	return nil
}

func (j *jpeg4Rows) dht(seg []byte) error {
	for len(seg) > 0 {
		tc, th := seg[0]>>4, seg[0]&15
		if tc > 1 || th > 3 || len(seg) < 17 {
			return e.Corrupt("jpeg huffman table")
		}
		var counts [17]int
		n := 0
		for k := 1; k <= 16; k++ {
			counts[k] = int(seg[k])
			n += counts[k]
		}
		if n > 256 || len(seg) < 17+n {
			return e.Corrupt("jpeg huffman table")
		}
		h := &huff{vals: append([]byte(nil), seg[17:17+n]...)}
		code, k := int32(0), 0
		for l := 1; l <= 16; l++ {
			h.valptr[l], h.mincode[l] = k, code
			code += int32(counts[l])
			k += counts[l]
			h.maxcode[l] = code - 1
			if counts[l] == 0 {
				h.maxcode[l] = -1
			}
			if code > 1<<l {
				return e.Corrupt("jpeg huffman table")
			}
			code <<= 1
		}
		h.maxcode[17] = 1<<31 - 1
		if tc == 0 {
			j.dc[th] = h
		} else {
			j.ac[th] = h
		}
		seg = seg[17+n:]
	}
	return nil
}

func (j *jpeg4Rows) sof(seg []byte) error {
	if len(seg) != 6+4*3 || seg[0] != 8 || seg[5] != 4 {
		return e.Unsupported("jpeg that is not 8-bit with 4 components")
	}
	if int(seg[1])<<8|int(seg[2]) != j.h || int(seg[3])<<8|int(seg[4]) != j.w {
		return e.Corrupt("jpeg frame does not match its chunk")
	}
	for c := range 4 {
		if seg[7+3*c] != 0x11 || seg[8+3*c] > 3 {
			return e.Unsupported("jpeg sampling") // libtiff refuses it too
		}
		j.comp[c].q = seg[8+3*c]
	}
	return nil
}

func (j *jpeg4Rows) sos(seg []byte) error {
	if len(seg) != 1+2*4+3 || seg[0] != 4 || seg[1+8] != 0 || seg[2+8] != 63 || seg[3+8] != 0 {
		return e.Unsupported("jpeg that is not one interleaved baseline scan")
	}
	for c := range 4 { // components come in frame order
		td, ta := seg[2+2*c]>>4, seg[2+2*c]&15
		if td > 3 || ta > 3 {
			return e.Corrupt("jpeg scan")
		}
		j.comp[c].td, j.comp[c].ta = td, ta
	}
	return nil
}

// bit returns the next n (1..16) bits.
func (j *jpeg4Rows) bits(n uint) (int32, error) {
	for j.n < n {
		var v byte
		if j.pos >= len(j.b) {
			return 0, e.Corrupt("truncated data")
		}
		v = j.b[j.pos]
		if v == 0xff {
			if j.pos+1 >= len(j.b) || j.b[j.pos+1] != 0 {
				return 0, e.Corrupt("jpeg marker in data")
			}
			j.pos++
		}
		j.pos++
		j.acc |= uint32(v) << (24 - j.n)
		j.n += 8
	}
	r := int32(j.acc >> (32 - n))
	j.acc <<= n
	j.n -= n
	return r, nil
}

func (j *jpeg4Rows) sym(h *huff) (byte, error) {
	if h == nil {
		return 0, e.Corrupt("jpeg table missing")
	}
	var code int32
	for l := 1; l <= 16; l++ {
		b, err := j.bits(1)
		if err != nil {
			return 0, err
		}
		code = code<<1 | b
		if h.maxcode[l] >= 0 && code <= h.maxcode[l] {
			k := h.valptr[l] + int(code-h.mincode[l])
			if k >= len(h.vals) {
				break
			}
			return h.vals[k], nil
		}
	}
	return 0, e.Corrupt("jpeg code")
}

func (j *jpeg4Rows) recv(s uint) (int32, error) {
	if s == 0 {
		return 0, nil
	}
	v, err := j.bits(s)
	if err != nil {
		return 0, err
	}
	if v < 1<<(s-1) {
		v -= 1<<s - 1
	}
	return v, nil
}

// block decodes one 8x8 block of component c into its dequantised coefficients.
func (j *jpeg4Rows) block(c int, out *[64]int32) error {
	*out = [64]int32{}
	t, err := j.sym(j.dc[j.comp[c].td])
	if err != nil || t > 11 {
		return e.Corrupt("jpeg data")
	}
	d, err := j.recv(uint(t))
	if err != nil {
		return err
	}
	j.pred[c] += d
	q := &j.qt[j.comp[c].q]
	out[0] = j.pred[c] * q[0]
	for k := 1; k < 64; {
		rs, err := j.sym(j.ac[j.comp[c].ta])
		if err != nil {
			return err
		}
		r, s := int(rs>>4), uint(rs&15)
		if s == 0 {
			if r != 15 {
				break
			}
			k += 16
			continue
		}
		k += r
		if k > 63 || s > 10 {
			return e.Corrupt("jpeg data")
		}
		v, err := j.recv(s)
		if err != nil {
			return err
		}
		out[zigzag[k]] = v * q[zigzag[k]]
		k++
	}
	return nil
}

// restartMarker byte-aligns and consumes an RSTn, resetting the predictors.
func (j *jpeg4Rows) restartMarker() error {
	j.acc, j.n = 0, 0
	if j.pos+2 > len(j.b) || j.b[j.pos] != 0xff || j.b[j.pos+1] < 0xd0 || j.b[j.pos+1] > 0xd7 {
		return e.Corrupt("jpeg restart")
	}
	j.pos += 2
	j.pred = [4]int32{}
	return nil
}

func (j *jpeg4Rows) decodeBand() error {
	var blk [64]int32
	for x := range j.mx {
		if j.restart > 0 {
			if j.left == 0 && (j.mcuRow > 0 || x > 0) {
				if err := j.restartMarker(); err != nil {
					return err
				}
			}
			if j.left == 0 {
				j.left = j.restart
			}
			j.left--
		}
		for c := range 4 {
			if err := j.block(c, &blk); err != nil {
				return err
			}
			idct(&blk, j.band[x*32+c:], j.mx*32)
		}
	}
	return nil
}

func (j *jpeg4Rows) ReadRow(dst []byte) error {
	if j.next >= j.h {
		return e.Corrupt("jpeg rows")
	}
	if j.next%8 == 0 {
		if err := j.ctx.Err(); err != nil {
			return err
		}
		if err := j.decodeBand(); err != nil {
			return err
		}
	}
	copy(dst, j.band[(j.next%8)*j.mx*32:][:4*j.w])
	j.next++
	return nil
}

// idct is jidctint.c's jpeg_idct_islow: out[(y*stride)+4*x] for the 8x8 block in.
const (
	cb   = 13
	pass = 2
)

func idct(in *[64]int32, out []byte, stride int) {
	var ws [64]int
	for c := range 8 {
		t10, t11, t12, t13, t0, t1, t2, t3 := idct1d(
			int(in[c]), int(in[8+c]), int(in[16+c]), int(in[24+c]), int(in[32+c]), int(in[40+c]), int(in[48+c]), int(in[56+c]))
		const s = cb - pass
		ws[c], ws[56+c] = ds(t10+t3, s), ds(t10-t3, s)
		ws[8+c], ws[48+c] = ds(t11+t2, s), ds(t11-t2, s)
		ws[16+c], ws[40+c] = ds(t12+t1, s), ds(t12-t1, s)
		ws[24+c], ws[32+c] = ds(t13+t0, s), ds(t13-t0, s)
	}
	for r := range 8 {
		w := ws[8*r:]
		t10, t11, t12, t13, t0, t1, t2, t3 := idct1d(w[0], w[1], w[2], w[3], w[4], w[5], w[6], w[7])
		const s = cb + pass + 3
		o := out[r*stride:]
		o[0], o[28] = rl(ds(t10+t3, s)), rl(ds(t10-t3, s))
		o[4], o[24] = rl(ds(t11+t2, s)), rl(ds(t11-t2, s))
		o[8], o[20] = rl(ds(t12+t1, s)), rl(ds(t12-t1, s))
		o[12], o[16] = rl(ds(t13+t0, s)), rl(ds(t13-t0, s))
	}
}

func ds(x int, n uint) int { return (x + 1<<(n-1)) >> n }

// rl is libjpeg's range_limit table indexed by x & 1023 (the level shift of 128 included).
func rl(x int) byte {
	m := x & 1023
	if m >= 512 {
		m -= 1024
	}
	return byte(max(0, min(255, m+128)))
}

// idct1d is one pass: the even part's tmp10..13 and the odd part's tmp0..3, scaled by 1<<cb.
func idct1d(i0, i1, i2, i3, i4, i5, i6, i7 int) (t10, t11, t12, t13, t0, t1, t2, t3 int) {
	z1 := (i2 + i6) * 4433
	t2 = z1 + i6*-15137
	t3 = z1 + i2*6270
	t0 = (i0 + i4) << cb
	t1 = (i0 - i4) << cb
	t10, t13, t11, t12 = t0+t3, t0-t3, t1+t2, t1-t2
	t0, t1, t2, t3 = i7, i5, i3, i1
	z1, z2, z3, z4 := t0+t3, t1+t2, t0+t2, t1+t3
	z5 := (z3 + z4) * 9633
	t0 *= 2446
	t1 *= 16819
	t2 *= 25172
	t3 *= 12299
	z1 *= -7373
	z2 *= -20995
	z3 = z3*-16069 + z5
	z4 = z4*-3196 + z5
	t0 += z1 + z3
	t1 += z2 + z4
	t2 += z2 + z3
	t3 += z1 + z4
	return
}
