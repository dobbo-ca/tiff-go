package tiff_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"testing"

	tiff "github.com/dobbo-ca/tiff-go"
)

// oobTIFF is a 1x1 8-bit grey TIFF, classic or BigTIFF, plus one extra tag
// whose count and value field are written as given.
func oobTIFF(big bool, tag, typ uint16, count, val uint64, more ...[4]uint64) []byte {
	le := binary.LittleEndian
	var b []byte
	if big {
		b = append([]byte("II+\x00\x08\x00\x00\x00"), make([]byte, 8)...)
	} else {
		b = []byte("II*\x00\x00\x00\x00\x00")
	}
	data := uint64(len(b))
	b = append(b, 7)
	if big {
		le.PutUint64(b[8:], uint64(len(b)))
	} else {
		le.PutUint32(b[4:], uint32(len(b)))
	}
	tags := [][4]uint64{{256, 4, 1, 1}, {257, 4, 1, 1}, {258, 3, 1, 8}, {262, 3, 1, 1},
		{273, 4, 1, data}, {278, 4, 1, 1}, {279, 4, 1, 1}, {uint64(tag), uint64(typ), count, val}}
	tags = append(tags, more...)
	if big {
		b = le.AppendUint64(b, uint64(len(tags)))
	} else {
		b = le.AppendUint16(b, uint16(len(tags)))
	}
	for _, t := range tags {
		b = le.AppendUint16(b, uint16(t[0]))
		b = le.AppendUint16(b, uint16(t[1]))
		if big {
			b = le.AppendUint64(le.AppendUint64(b, t[2]), t[3])
		} else {
			b = le.AppendUint32(le.AppendUint32(b, uint32(t[2])), uint32(t[3]))
		}
	}
	if big {
		return le.AppendUint64(b, 0)
	}
	return le.AppendUint32(b, 0)
}

func TestSkippedOutOfRangeTags(t *testing.T) {
	types := map[string]uint16{"BYTE": 1, "ASCII": 2, "SHORT": 3, "LONG": 4, "RATIONAL": 5, "SBYTE": 6,
		"UNDEFINED": 7, "SSHORT": 8, "SLONG": 9, "SRATIONAL": 10, "FLOAT": 11, "DOUBLE": 12, "IFD": 13,
		"LONG8": 16, "SLONG8": 17, "IFD8": 18}
	for name, typ := range types {
		for _, big := range []bool{false, true} {
			vw := uint64(4)
			if big {
				vw = 8
			}
			sz := map[uint16]uint64{1: 1, 2: 1, 6: 1, 7: 1, 3: 2, 8: 2, 4: 4, 9: 4, 11: 4, 13: 4}[typ]
			if sz == 0 {
				sz = 8
			}
			for kind, cv := range map[string][2]uint64{"count": {1 << 30, 0}, "offset": {vw/sz + 1, 1 << 20}} {
				t.Run(fmt.Sprintf("%s/big=%v/%s", name, big, kind), func(t *testing.T) {
					b := oobTIFF(big, 700, typ, cv[0], cv[1])
					got, _, err := decode(t, b, tiff.Limits{}, 0)
					if err != nil || !bytes.Equal(got, []byte{7}) {
						t.Fatalf("%v %v", got, err)
					}
					f, _ := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
					p, _ := f.Page(0)
					if s := p.Info().Skipped; !slices.Equal(s, []uint16{700}) {
						t.Fatalf("skipped %v", s)
					}
				})
			}
		}
	}
}

func TestSkippedOrientationReadsAsOne(t *testing.T) {
	for _, big := range []bool{false, true} {
		b := oobTIFF(big, 274, 3, 1<<30, 0)
		f, _ := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
		p, err := f.Page(0)
		if err != nil || p.Info().Orientation != 1 || !slices.Equal(p.Info().Skipped, []uint16{274}) {
			t.Fatalf("big=%v %v %+v", big, err, p.Info())
		}
	}
}

func TestOutOfRangeNeededTagStaysCorrupt(t *testing.T) {
	for _, big := range []bool{false, true} {
		for _, c := range [][2]uint64{{1 << 30, 0}, {100, 1 << 20}} {
			for _, tag := range []uint16{338, 339} { // ExtraSamples, SampleFormat
				b := oobTIFF(big, tag, 3, c[0], c[1])
				if _, _, err := decode(t, b, tiff.Limits{}, 0); !errors.Is(err, tiff.ErrCorrupt) {
					t.Fatalf("big=%v tag %d %v: %v", big, tag, c, err)
				}
			}
		}
	}
}

// Tags the page does not read are skipped even when decode-relevant in
// general: ColorMap without Palette, JPEGTables without JPEG, tile tables on a
// strip file, CCITT options on an uncompressed file, FillOrder.
func TestSkippedTagsThisPageDoesNotRead(t *testing.T) {
	for _, big := range []bool{false, true} {
		for _, tag := range []uint16{320, 347, 292, 293, 324, 325, 266} {
			for _, cv := range [][2]uint64{{1 << 30, 0}, {3, 1 << 20}} {
				b := oobTIFF(big, tag, 4, cv[0], cv[1])
				got, _, err := decode(t, b, tiff.Limits{}, 0)
				if err != nil || !bytes.Equal(got, []byte{7}) {
					t.Fatalf("big=%v tag %d %v: %v %v", big, tag, cv, got, err)
				}
			}
		}
	}
}

func TestDuplicateOrientationLastWins(t *testing.T) {
	// a valid Orientation=6 followed by an out-of-range one reads as 1 and is skipped.
	b := oobTIFF(false, 274, 3, 1, 6, [4]uint64{274, 3, 1 << 30, 0})
	f, _ := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
	p, err := f.Page(0)
	if err != nil || p.Info().Orientation != 1 || !slices.Equal(p.Info().Skipped, []uint16{274}) {
		t.Fatalf("%v %+v", err, p.Info())
	}
}

func TestSkippedOnceAndTiledIgnoresRowsPerStrip(t *testing.T) {
	b := oobTIFF(false, 700, 3, 1<<30, 0, [4]uint64{700, 3, 1 << 30, 0})
	f, _ := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
	if p, err := f.Page(0); err != nil || !slices.Equal(p.Info().Skipped, []uint16{700}) {
		t.Fatalf("%v %+v", err, p.Info())
	}
	// tile tags make 278 unread: libtiff-style skip, here reported
	b = oobTIFF(false, 278, 4, 1<<30, 0, [4]uint64{322, 4, 1, 1}, [4]uint64{323, 4, 1, 1}, [4]uint64{324, 4, 1, 8}, [4]uint64{325, 4, 1, 1})
	f, _ = tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
	if p, err := f.Page(0); err != nil || !slices.Contains(p.Info().Skipped, 278) {
		t.Fatalf("%v", err)
	}
}
