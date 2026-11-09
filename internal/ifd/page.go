package ifd

import "github.com/dobbo-ca/tiff-go/internal/e"

// Compression codes this package decodes.
const (
	None     = 1
	CCITT3   = 3
	CCITT4   = 4
	LZW      = 5
	JPEG     = 7
	Deflate  = 8
	DeflateX = 32946 // Adobe's code for the same zlib stream
	PackBits = 32773
)

// Photometric values this package decodes (YCbCr only inside JPEG).
const (
	WhiteIsZero = 0
	BlackIsZero = 1
	RGB         = 2
	Palette     = 3
	CMYK        = 5
	YCbCr       = 6
)

// Page is one parsed IFD.
type Page struct {
	F                        *File
	Width, Height            int
	BPS, SPP                 int
	Photometric, Compression int
	Predictor, Orientation   int
	T4, T6                   uint64
	ColorMap                 []uint16
	ExtraSamples             []uint16
	JPEGTables               []byte
	Skipped                  []uint16 // tags dropped for a count or offset outside the file
	TileW, TileL             int      // zero for strips
	RowsPerStrip             int
	Offsets, Counts          Entry
	across, down, nStrips    int
}

// Chunks reports how many strips or tiles the page has.
func (p *Page) Chunks() int {
	if p.TileW > 0 {
		return p.across * p.down
	}
	return p.nStrips
}

// TilesAcross reports the tiles per tile row (zero for strips).
func (p *Page) TilesAcross() int { return p.across }

func (f *File) one(m map[uint16]Entry, tag uint16, def uint64) (uint64, error) {
	en, ok := m[tag]
	if !ok {
		return def, nil
	}
	if en.Count != 1 {
		return 0, e.Corrupt("tag %d count", tag)
	}
	return f.Val(en, 0)
}

// Page parses IFD i. maxPixels caps Width*Height.
func (f *File) Page(i int, maxPixels int64) (*Page, error) {
	m, skipped, err := f.entries(f.Offs[i])
	if err != nil {
		return nil, err
	}
	p := &Page{F: f, Skipped: skipped}
	var v [12]uint64
	for k, t := range [...]struct {
		tag uint16
		def uint64
	}{{256, 0}, {257, 0}, {259, 1}, {262, 1 << 20}, {274, 1}, {277, 1}, {278, 1<<32 - 1},
		{284, 1}, {317, 1}, {266, 1}, {322, 0}, {323, 0}} {
		if v[k], err = f.one(m, t.tag, t.def); err != nil {
			return nil, err
		}
	}
	w, h, comp, photo, orient, spp, rps, planar, pred, fill, tw, tl := v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7], v[8], v[9], v[10], v[11]
	if w == 0 || h == 0 || w > 1<<31 || h > 1<<31 {
		return nil, e.Corrupt("dimensions")
	}
	if px := int64(w) * int64(h); px > maxPixels {
		return nil, &e.LimitError{Name: "pixels", Got: px, Max: maxPixels}
	}
	if photo == 1<<20 {
		return nil, e.Corrupt("missing photometric")
	}
	if spp == 0 || spp > 16 {
		return nil, e.Unsupported("samples per pixel %d", spp)
	}
	p.Width, p.Height, p.SPP, p.Compression, p.Photometric = int(w), int(h), int(spp), int(comp), int(photo)
	p.Orientation, p.Predictor = int(orient), int(pred)

	switch comp {
	case None, CCITT3, CCITT4, LZW, JPEG, Deflate, DeflateX, PackBits:
	default:
		return nil, e.Unsupported("compression %d", comp)
	}
	minSPP := map[uint64]uint64{WhiteIsZero: 1, BlackIsZero: 1, RGB: 3, Palette: 1, CMYK: 4, YCbCr: 3}[photo]
	if minSPP == 0 || (photo == YCbCr && comp != JPEG) {
		return nil, e.Unsupported("photometric %d", photo)
	}
	if spp < minSPP {
		return nil, e.Corrupt("samples per pixel for photometric %d", photo)
	}
	if planar != 1 {
		return nil, e.Unsupported("planar configuration %d", planar)
	}
	if fill != 1 && fill != 2 {
		return nil, e.Unsupported("fill order %d", fill)
	}
	if pred != 1 && pred != 2 {
		return nil, e.Unsupported("predictor %d", pred)
	}
	if en, ok := m[339]; ok {
		sf, err := f.Ints(en, 16)
		if err != nil {
			return nil, err
		}
		for _, s := range sf {
			if s != 1 {
				return nil, e.Unsupported("sample format %d", s)
			}
		}
	}

	p.BPS = 1
	if en, ok := m[258]; ok {
		bs, err := f.Ints(en, 16)
		if err != nil {
			return nil, err
		}
		if len(bs) != 1 && len(bs) != int(spp) {
			return nil, e.Corrupt("bits per sample count")
		}
		for _, b := range bs {
			if b != bs[0] {
				return nil, e.Unsupported("mixed bits per sample")
			}
		}
		if bs[0] < 1 || bs[0] > 16 {
			return nil, e.Unsupported("bits per sample %d", bs[0])
		}
		p.BPS = int(bs[0])
	}
	if fill == 2 && p.BPS < 8 { // libtiff ignores it at 8 bits and up
		return nil, e.Unsupported("fill order %d at %d bits", fill, p.BPS)
	}
	if pred == 2 && p.BPS != 8 && p.BPS != 16 {
		return nil, e.Unsupported("predictor at %d bits", p.BPS)
	}
	if comp == JPEG && p.BPS != 8 {
		return nil, e.Unsupported("jpeg at %d bits", p.BPS)
	}
	if photo == Palette {
		en, ok := m[320]
		if !ok || p.BPS > 8 || en.Count != 3<<p.BPS {
			return nil, e.Corrupt("colour map")
		}
		cm, err := f.Ints(en, 3<<8)
		if err != nil {
			return nil, err
		}
		p.ColorMap = make([]uint16, len(cm))
		for i, c := range cm {
			p.ColorMap[i] = uint16(c)
		}
	}
	if en, ok := m[338]; ok {
		xs, err := f.Ints(en, 16)
		if err != nil {
			return nil, err
		}
		for _, x := range xs {
			p.ExtraSamples = append(p.ExtraSamples, uint16(x))
		}
	}
	if en, ok := m[347]; ok && comp == JPEG {
		if en.Count > 1<<20 {
			return nil, &e.LimitError{Name: "jpeg tables", Got: int64(min(en.Count, 1<<62)), Max: 1 << 20}
		}
		if p.JPEGTables, err = f.Bytes(en); err != nil {
			return nil, err
		}
	}
	if en, ok := m[292]; ok {
		p.T4, _ = f.Val(en, 0)
	}
	if en, ok := m[293]; ok {
		p.T6, _ = f.Val(en, 0)
	}

	offTag, cntTag := uint16(273), uint16(279)
	var need uint64
	if tw != 0 || tl != 0 {
		offTag, cntTag = 324, 325
		if tw == 0 || tl == 0 || tw > 1<<31 || tl > 1<<31 || (tw*spp*uint64(p.BPS))%8 != 0 {
			return nil, e.Corrupt("tile size")
		}
		p.TileW, p.TileL = int(tw), int(tl)
		p.across, p.down = int((w+tw-1)/tw), int((h+tl-1)/tl)
		need = uint64(p.across) * uint64(p.down)
	} else {
		if rps == 0 {
			return nil, e.Corrupt("rows per strip")
		}
		p.RowsPerStrip = int(min(rps, h))
		p.nStrips = int((h + uint64(p.RowsPerStrip) - 1) / uint64(p.RowsPerStrip))
		need = uint64(p.nStrips)
	}
	var ok1, ok2 bool
	p.Offsets, ok1 = m[offTag]
	p.Counts, ok2 = m[cntTag]
	if !ok1 || !ok2 || p.Offsets.Count < need || p.Counts.Count < need {
		return nil, e.Corrupt("chunk table")
	}
	return p, nil
}

// RowBytes is the packed size of one full-width row.
func (p *Page) RowBytes() int64 {
	return (int64(p.Width)*int64(p.SPP)*int64(p.BPS) + 7) / 8
}
