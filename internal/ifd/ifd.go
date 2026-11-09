// Package ifd parses the TIFF container: header, IFD chain and tag values.
// It never allocates in proportion to a count read from the file.
package ifd

import (
	"encoding/binary"
	"io"

	"github.com/dobbo-ca/tiff-go/internal/e"
)

// maxEntries bounds one IFD's tag count (tags are unique, so a real IFD
// holds a few dozen).
const maxEntries = 4096

// File is an opened container: byte order, variant and the IFD chain.
type File struct {
	ra   io.ReaderAt
	size int64
	bo   binary.ByteOrder
	big  bool
	// Offs holds each IFD's offset in chain order.
	Offs []int64
}

// ByteOrder reports the file's byte order.
func (f *File) ByteOrder() binary.ByteOrder { return f.bo }

func (f *File) readAt(p []byte, off int64) error {
	if off < 0 || off > f.size || int64(len(p)) > f.size-off {
		return e.Corrupt("read outside file")
	}
	if _, err := f.ra.ReadAt(p, off); err != nil && (err != io.EOF || len(p) == 0) {
		return err
	}
	return nil
}

func (f *File) uint(b []byte) uint64 {
	switch len(b) {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(f.bo.Uint16(b))
	case 4:
		return uint64(f.bo.Uint32(b))
	}
	return f.bo.Uint64(b)
}

// Open reads the header and walks the IFD chain, refusing loops and more
// than maxIFDs directories. No directory body is parsed.
func Open(ra io.ReaderAt, size int64, maxIFDs int) (*File, error) {
	f := &File{ra: ra, size: size}
	var h [16]byte
	if size < 8 {
		return nil, e.Corrupt("short header")
	}
	if err := f.readAt(h[:min(size, 16)], 0); err != nil {
		return nil, err
	}
	switch string(h[:2]) {
	case "II":
		f.bo = binary.LittleEndian
	case "MM":
		f.bo = binary.BigEndian
	default:
		return nil, e.Corrupt("byte order mark")
	}
	var first uint64
	switch f.bo.Uint16(h[2:]) {
	case 42:
		first = uint64(f.bo.Uint32(h[4:]))
	case 43:
		if size < 16 || f.bo.Uint16(h[4:]) != 8 || f.bo.Uint16(h[6:]) != 0 {
			return nil, e.Corrupt("bigtiff header")
		}
		f.big = true
		first = f.bo.Uint64(h[8:])
	default:
		return nil, e.Corrupt("version")
	}
	seen := map[uint64]struct{}{}
	for off := first; off != 0; {
		if _, dup := seen[off]; dup {
			return nil, e.Corrupt("ifd chain loop")
		}
		if len(f.Offs) >= maxIFDs {
			return nil, &e.LimitError{Name: "ifds", Got: int64(len(f.Offs)) + 1, Max: int64(maxIFDs)}
		}
		seen[off] = struct{}{}
		if off >= uint64(size) {
			return nil, e.Corrupt("ifd offset")
		}
		f.Offs = append(f.Offs, int64(off))
		n, start, err := f.count(int64(off))
		if err != nil {
			return nil, err
		}
		osz := f.offSize()
		var b [8]byte
		if err := f.readAt(b[:osz], start+n*f.entrySize()); err != nil {
			return nil, err
		}
		off = f.uint(b[:osz])
	}
	if len(f.Offs) == 0 {
		return nil, e.Corrupt("no ifd")
	}
	return f, nil
}

func (f *File) offSize() int64 {
	if f.big {
		return 8
	}
	return 4
}

func (f *File) entrySize() int64 {
	if f.big {
		return 20
	}
	return 12
}

// count returns the entry count and the offset of the first entry.
func (f *File) count(off int64) (n, start int64, err error) {
	var b [8]byte
	w := int64(2)
	if f.big {
		w = 8
	}
	if err = f.readAt(b[:w], off); err != nil {
		return
	}
	c := f.uint(b[:w])
	if c > maxEntries {
		return 0, 0, &e.LimitError{Name: "ifd entries", Got: int64(min(c, 1<<62)), Max: maxEntries}
	}
	if c == 0 {
		return 0, 0, e.Corrupt("empty ifd")
	}
	n, start = int64(c), off+w
	if need := w + n*f.entrySize() + f.offSize(); off > f.size-need {
		return 0, 0, e.Corrupt("ifd past end of file")
	}
	return
}

// Entry is one directory entry.
type Entry struct {
	Tag, Type uint16
	Count     uint64
	sz        uint64
	inline    [8]byte
	isInline  bool
	off       int64
	bad       bool // count or offset outside the file
}

func typeSize(t uint16) uint64 {
	switch t {
	case 1, 2, 6, 7:
		return 1
	case 3, 8:
		return 2
	case 4, 9, 11, 13:
		return 4
	case 5, 10, 12, 16, 17, 18:
		return 8
	}
	return 0
}

// entries returns the directory's tags and, in file order, the ids of those
// whose count or offset lies outside the file. Such a tag is kept with bad
// set: reading its value is corrupt, so only the tags a page reads can make
// it corrupt. The Orientation and XMP entries come back too, unmapped and in
// file order, whatever their type.
func (f *File) entries(off int64) (map[uint16]Entry, []uint16, []Entry, error) {
	n, start, err := f.count(off)
	if err != nil {
		return nil, nil, nil, err
	}
	buf := make([]byte, n*f.entrySize())
	if err := f.readAt(buf, start); err != nil {
		return nil, nil, nil, err
	}
	var skipped []uint16
	var tagged []Entry
	m := make(map[uint16]Entry, n)
	vw := f.offSize()
	for i := int64(0); i < n; i++ {
		b := buf[i*f.entrySize():]
		en := Entry{Tag: f.bo.Uint16(b), Type: f.bo.Uint16(b[2:])}
		if f.big {
			en.Count, b = f.bo.Uint64(b[4:]), b[12:]
		} else {
			en.Count, b = uint64(f.bo.Uint32(b[4:])), b[8:]
		}
		// Range checks divide instead of multiplying: Count*sz can wrap.
		switch en.sz = typeSize(en.Type); {
		case en.sz == 0: // unknown type: ignore the tag
		case en.Count <= uint64(vw)/en.sz:
			en.isInline = true
			copy(en.inline[:], b[:vw])
		default:
			o := f.uint(b[:vw])
			if en.bad = o > uint64(f.size) || en.Count > (uint64(f.size)-o)/en.sz; !en.bad {
				en.off = int64(o)
			}
		}
		if en.bad {
			skipped = append(skipped, en.Tag)
		}
		if en.Tag == 274 || en.Tag == 700 {
			tagged = append(tagged, en)
		}
		if en.sz != 0 {
			m[en.Tag] = en
		}
	}
	return m, skipped, tagged, nil
}

// Val returns integer element i of an unsigned-integer entry (BYTE, SHORT,
// LONG, LONG8), reading it from the file only when it is not inline.
func (f *File) Val(en Entry, i uint64) (uint64, error) {
	if en.bad {
		return 0, e.Corrupt("tag %d value outside file", en.Tag)
	}
	switch en.Type {
	case 1, 3, 4, 16:
	default:
		return 0, e.Corrupt("tag %d type", en.Tag)
	}
	if i >= en.Count {
		return 0, e.Corrupt("tag %d index", en.Tag)
	}
	if en.isInline {
		return f.uint(en.inline[i*en.sz : (i+1)*en.sz]), nil
	}
	var b [8]byte
	if err := f.readAt(b[:en.sz], en.off+int64(i*en.sz)); err != nil {
		return 0, err
	}
	return f.uint(b[:en.sz]), nil
}

// Bytes returns an entry's whole value; the caller has bounded Count.
func (f *File) Bytes(en Entry) ([]byte, error) {
	if en.bad {
		return nil, e.Corrupt("tag %d value outside file", en.Tag)
	}
	n := en.Count * en.sz
	if en.isInline {
		return en.inline[:n], nil
	}
	b := make([]byte, n)
	return b, f.readAt(b, en.off)
}

// Ints returns an unsigned-integer entry with at most max elements.
func (f *File) Ints(en Entry, max uint64) ([]uint64, error) {
	if en.Count > max {
		return nil, e.Corrupt("tag %d count", en.Tag)
	}
	switch en.Type {
	case 1, 3, 4, 16:
	default:
		return nil, e.Corrupt("tag %d type", en.Tag)
	}
	b, err := f.Bytes(en)
	if err != nil {
		return nil, err
	}
	out := make([]uint64, en.Count)
	for i := range out {
		out[i] = f.uint(b[uint64(i)*en.sz : (uint64(i)+1)*en.sz])
	}
	return out, nil
}

// Chunk returns the validated file range of strip or tile i.
func (f *File) Chunk(offs, cnts Entry, i uint64) (off, n int64, err error) {
	o, err := f.Val(offs, i)
	if err != nil {
		return 0, 0, err
	}
	c, err := f.Val(cnts, i)
	if err != nil {
		return 0, 0, err
	}
	if o > uint64(f.size) || c > uint64(f.size)-o {
		return 0, 0, e.Corrupt("chunk %d outside file", i)
	}
	return int64(o), int64(c), nil
}

// Section returns a reader over [off, off+n).
func (f *File) Section(off, n int64) *io.SectionReader { return io.NewSectionReader(f.ra, off, n) }
