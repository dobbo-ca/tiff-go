// Package fixture writes synthetic TIFF files for tests: every layout and
// compression the decoder reads, from a deterministic pixel pattern.
package fixture

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"image"

	"github.com/dobbo-ca/jpeg-go"
)

// Page describes one IFD to write.
type Page struct {
	W, H, SPP, BPS int
	Photometric    int
	Compression    int // TIFF code; 1 when zero
	Predictor      int
	RowsPerStrip   int // strips (default H) unless TileW is set
	TileW, TileL   int
	ColorMap       []uint16
	Pix            []byte   // packed big-endian rows; Pattern when nil
	Chunks         [][]byte // pre-compressed strips or tiles, bypassing the encoder
	T4, T6         uint32
	JPEGTables     []byte
}

// File describes a whole TIFF.
type File struct {
	LittleEndian, Big bool
	Pages             []Page
}

// Pattern is a deterministic image: rows of packed samples, 16-bit big-endian.
func Pattern(w, h, spp, bps int) []byte {
	rb := (w*spp*bps + 7) / 8
	b := make([]byte, rb*h)
	for i := range b {
		y, x := i/rb, i%rb
		b[i] = byte(x*7 + y*13 + x*y)
		if bps < 8 && spp == 1 { // keep bilevel and low-depth data varied but legal
			b[i] = byte((x*5+y*3)*31) ^ byte(y)
		}
	}
	return b
}

type entry struct {
	tag, typ uint16
	vals     []uint64
}

type writer struct {
	bytes.Buffer
	bo  order
	big bool
}

type order interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

func (w *writer) u16(v uint16) { w.Write(w.bo.AppendUint16(nil, v)) }
func (w *writer) u32(v uint32) { w.Write(w.bo.AppendUint32(nil, v)) }
func (w *writer) u64(v uint64) { w.Write(w.bo.AppendUint64(nil, v)) }
func (w *writer) off(v uint64) {
	if w.big {
		w.u64(v)
	} else {
		w.u32(uint32(v))
	}
}
func (w *writer) align() {
	if w.Len()%2 == 1 {
		w.WriteByte(0)
	}
}

func size(t uint16) int { return map[uint16]int{1: 1, 3: 2, 4: 4, 7: 1, 16: 8}[t] }

func (w *writer) val(t uint16, v uint64) {
	switch t {
	case 1, 7:
		w.WriteByte(byte(v))
	case 3:
		w.u16(uint16(v))
	case 4:
		w.u32(uint32(v))
	case 16:
		w.u64(v)
	}
}

// Bytes renders the file.
func (f File) Bytes() []byte {
	w := &writer{bo: binary.BigEndian, big: f.Big}
	if f.LittleEndian {
		w.bo = binary.LittleEndian
	}
	if f.LittleEndian {
		w.WriteString("II")
	} else {
		w.WriteString("MM")
	}
	if f.Big {
		w.u16(43)
		w.u16(8)
		w.u16(0)
	} else {
		w.u16(42)
	}
	patch := w.Len() // where the next IFD offset goes
	w.off(0)
	for _, p := range f.Pages {
		patch = w.page(p, patch)
	}
	return w.Bytes()
}

// page writes chunks, value arrays and the IFD, pointing the field at patch
// to it; it returns the position of the IFD's own next-offset field.
func (w *writer) page(p Page, patch int) int {
	if p.Compression == 0 {
		p.Compression = 1
	}
	if p.Pix == nil {
		p.Pix = Pattern(p.W, p.H, p.SPP, p.BPS)
	}
	chunks := p.Chunks
	if chunks == nil {
		chunks = w.encode(p)
	}
	offs, cnts := make([]uint64, len(chunks)), make([]uint64, len(chunks))
	for i, c := range chunks {
		w.align()
		offs[i], cnts[i] = uint64(w.Len()), uint64(len(c))
		w.Write(c)
	}
	sv := func(v ...int) []uint64 {
		o := make([]uint64, len(v))
		for i := range v {
			o[i] = uint64(v[i])
		}
		return o
	}
	bps := make([]int, p.SPP)
	for i := range bps {
		bps[i] = p.BPS
	}
	es := []entry{
		{256, 4, sv(p.W)}, {257, 4, sv(p.H)}, {258, 3, sv(bps...)}, {259, 3, sv(p.Compression)},
		{262, 3, sv(p.Photometric)}, {277, 3, sv(p.SPP)},
	}
	if p.Predictor != 0 {
		es = append(es, entry{317, 3, sv(p.Predictor)})
	}
	if p.T4 != 0 {
		es = append(es, entry{292, 4, sv(int(p.T4))})
	}
	if p.T6 != 0 {
		es = append(es, entry{293, 4, sv(int(p.T6))})
	}
	if p.ColorMap != nil {
		cm := make([]uint64, len(p.ColorMap))
		for i, c := range p.ColorMap {
			cm[i] = uint64(c)
		}
		es = append(es, entry{320, 3, cm})
	}
	if p.JPEGTables != nil {
		t := make([]uint64, len(p.JPEGTables))
		for i, c := range p.JPEGTables {
			t[i] = uint64(c)
		}
		es = append(es, entry{347, 7, t})
	}
	ot, ct := uint16(4), uint16(4)
	if w.big {
		ot = 16
	}
	if p.TileW > 0 {
		es = append(es, entry{322, 4, sv(p.TileW)}, entry{323, 4, sv(p.TileL)}, entry{324, ot, offs}, entry{325, ct, cnts})
	} else {
		rps := p.RowsPerStrip
		if rps == 0 {
			rps = p.H
		}
		es = append(es, entry{273, ot, offs}, entry{278, 4, sv(rps)}, entry{279, ct, cnts})
	}
	// out-of-line values first, then the directory; tags sorted.
	for i := range es {
		for j := i + 1; j < len(es); j++ {
			if es[j].tag < es[i].tag {
				es[i], es[j] = es[j], es[i]
			}
		}
	}
	inl := 4
	if w.big {
		inl = 8
	}
	where := make([]uint64, len(es))
	for i, en := range es {
		if len(en.vals)*size(en.typ) > inl {
			w.align()
			where[i] = uint64(w.Len())
			for _, v := range en.vals {
				w.val(en.typ, v)
			}
		}
	}
	w.align()
	if w.big {
		w.bo.PutUint64(w.Bytes()[patch:], uint64(w.Len()))
	} else {
		w.bo.PutUint32(w.Bytes()[patch:], uint32(w.Len()))
	}
	return w.directory(es, where, inl)
}

func (w *writer) directory(es []entry, where []uint64, inl int) int {
	if w.big {
		w.u64(uint64(len(es)))
	} else {
		w.u16(uint16(len(es)))
	}
	for i, en := range es {
		w.u16(en.tag)
		w.u16(en.typ)
		w.off(uint64(len(en.vals)))
		if where[i] != 0 {
			w.off(where[i])
			continue
		}
		start := w.Len()
		for _, v := range en.vals {
			w.val(en.typ, v)
		}
		for w.Len() < start+inl {
			w.WriteByte(0)
		}
	}
	at := w.Len()
	w.off(0)
	return at
}

// encode cuts p.Pix into strips or tiles and compresses each.
func (w *writer) encode(p Page) [][]byte {
	rb := (p.W*p.SPP*p.BPS + 7) / 8
	var out [][]byte
	chunk := func(rows [][]byte, cw int) {
		out = append(out, w.compress(p, rows, cw))
	}
	if p.TileW > 0 {
		trb := (p.TileW*p.SPP*p.BPS + 7) / 8
		for y := 0; y < p.H; y += p.TileL {
			for x := 0; x < p.W; x += p.TileW {
				rows := make([][]byte, p.TileL)
				for r := range rows {
					rows[r] = make([]byte, trb)
					if y+r < p.H {
						copy(rows[r], p.Pix[(y+r)*rb+x*trb/p.TileW:(y+r+1)*rb])
					}
				}
				chunk(rows, p.TileW)
			}
		}
		return out
	}
	rps := p.RowsPerStrip
	if rps == 0 {
		rps = p.H
	}
	for y := 0; y < p.H; y += rps {
		var rows [][]byte
		for r := y; r < min(y+rps, p.H); r++ {
			rows = append(rows, append([]byte(nil), p.Pix[r*rb:(r+1)*rb]...))
		}
		chunk(rows, p.W)
	}
	return out
}

func (w *writer) compress(p Page, rows [][]byte, cw int) []byte {
	if p.Compression == 7 {
		return jpegChunk(p, rows, cw)
	}
	var raw []byte
	for _, r := range rows {
		if p.BPS == 16 && w.bo == binary.LittleEndian {
			for i := 0; i+1 < len(r); i += 2 {
				r[i], r[i+1] = r[i+1], r[i]
			}
		}
		if p.Predictor == 2 {
			for i := cw*p.SPP - 1; i >= p.SPP; i-- {
				if p.BPS == 8 {
					r[i] -= r[i-p.SPP]
				} else {
					w.bo.PutUint16(r[2*i:], w.bo.Uint16(r[2*i:])-w.bo.Uint16(r[2*(i-p.SPP):]))
				}
			}
		}
		raw = append(raw, r...)
	}
	switch p.Compression {
	case 5:
		return lzwEncode(raw)
	case 8, 32946:
		var b bytes.Buffer
		z := zlib.NewWriter(&b)
		_, _ = z.Write(raw)
		_ = z.Close()
		return b.Bytes()
	case 32773:
		return packBits(raw)
	}
	return raw
}

func jpegChunk(p Page, rows [][]byte, cw int) []byte {
	var img image.Image
	if p.SPP == 1 {
		g := image.NewGray(image.Rect(0, 0, cw, len(rows)))
		for y, r := range rows {
			copy(g.Pix[y*g.Stride:], r)
		}
		img = g
	} else {
		m := image.NewRGBA(image.Rect(0, 0, cw, len(rows)))
		for y, r := range rows {
			for x := 0; x < cw; x++ {
				copy(m.Pix[y*m.Stride+4*x:], r[3*x:3*x+3])
				m.Pix[y*m.Stride+4*x+3] = 255
			}
		}
		img = m
	}
	var b bytes.Buffer
	if err := jpeg.Encode(context.Background(), &b, img, &jpeg.EncodeOptions{Quality: 95, Chroma444: true}); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func packBits(b []byte) []byte {
	var o []byte
	for i := 0; i < len(b); {
		n := 1
		for i+n < len(b) && b[i+n] == b[i] && n < 128 {
			n++
		}
		if n >= 3 {
			o = append(o, byte(257-n), b[i])
			i += n
			continue
		}
		j := i
		for j < len(b) && j-i < 128 && (j+2 >= len(b) || b[j] != b[j+1] || b[j] != b[j+2]) {
			j++
		}
		if j == i {
			j++
		}
		o = append(o, byte(j-i-1))
		o = append(o, b[i:j]...)
		i = j
	}
	return o
}

func lzwEncode(b []byte) []byte {
	var out []byte
	var acc uint32
	var nb uint
	width, next := uint(9), 258
	emit := func(c int) {
		acc = acc<<width | uint32(c)
		nb += width
		for nb >= 8 {
			nb -= 8
			out = append(out, byte(acc>>nb))
		}
	}
	grow := func() {
		if next >= 1<<width && width < 12 {
			width++
		}
	}
	dict := map[uint32]int{}
	emit(256)
	w := -1
	for _, c := range b {
		if w < 0 {
			w = int(c)
			continue
		}
		if code, ok := dict[uint32(w)<<8|uint32(c)]; ok {
			w = code
			continue
		}
		grow()
		emit(w)
		dict[uint32(w)<<8|uint32(c)] = next
		next++
		w = int(c)
		if next == 4094 {
			emit(256)
			dict, width, next = map[uint32]int{}, 9, 258
		}
	}
	if w >= 0 {
		grow()
		emit(w)
		next++
	}
	grow()
	emit(257)
	if nb > 0 {
		out = append(out, byte(acc<<(8-nb)))
	}
	return out
}
