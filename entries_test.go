package tiff_test

import (
	"bytes"
	"fmt"
	"slices"
	"testing"

	tiff "github.com/dobbo-ca/tiff-go"
)

func page0(t *testing.T, b []byte, lim tiff.Limits) *tiff.Page {
	t.Helper()
	f, err := tiff.Open(bytes.NewReader(b), int64(len(b)), lim)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.Page(0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOrientationEntries(t *testing.T) {
	for _, big := range []bool{false, true} {
		b := oobTIFF(big, 274, 3, 1, 6, [4]uint64{274, 3, 1, 8}, [4]uint64{274, 12, 1, 0}, [4]uint64{274, 3, 1 << 30, 0})
		p := page0(t, b, tiff.Limits{})
		want := []tiff.TagEntry{
			{Type: 3, Count: 1, Value: 6, Duplicate: true},
			{Type: 3, Count: 1, Value: 8, Duplicate: true},
			{Type: 12, Count: 1, BadType: true, Duplicate: true},
			{Type: 3, Count: 1 << 30, OutOfRange: true, Duplicate: true},
		}
		if !slices.EqualFunc(p.Orientations, want, func(a, b tiff.TagEntry) bool {
			return a.Type == b.Type && a.Count == b.Count && a.Value == b.Value && a.OutOfRange == b.OutOfRange &&
				a.BadType == b.BadType && a.Duplicate == b.Duplicate && a.Data == nil
		}) {
			t.Fatalf("big=%v %+v", big, p.Orientations)
		}
		if !slices.Equal(p.Info().Skipped, []uint16{274}) {
			t.Fatalf("skipped %v", p.Info().Skipped)
		}
	}
}

func TestXMPEntries(t *testing.T) {
	for _, big := range []bool{false, true} {
		// inline (4 bytes), out of range, wrong type, over the cap
		b := oobTIFF(big, 700, 1, 4, 0x34333231, [4]uint64{700, 1, 1 << 30, 0}, [4]uint64{700, 3, 1, 1}, [4]uint64{700, 1, 9, 0})
		p := page0(t, b, tiff.Limits{MaxMetadataBytes: 8})
		x := p.XMPs
		if len(x) != 4 || string(x[0].Data) != "1234" || x[0].OutOfRange || !x[0].Duplicate ||
			!x[1].OutOfRange || x[1].Data != nil || !x[2].BadType || x[2].Data != nil ||
			!x[3].OverLimit || x[3].Data != nil {
			t.Fatalf("big=%v %+v", big, x)
		}
	}
	p := page0(t, oobTIFF(false, 700, 7, 2, 0x4241), tiff.Limits{})
	if len(p.XMPs) != 1 || string(p.XMPs[0].Data) != "AB" || p.XMPs[0].Duplicate || len(p.Orientations) != 0 {
		t.Fatalf("%+v", p.XMPs)
	}
}

// Counts near the maximum and offsets near 2^63 and 2^64 never wrap: the
// entry is out of range, listed, and skipped for decoding.
func TestEntriesOverflowSafe(t *testing.T) {
	for _, typ := range []uint16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 16, 17, 18} {
		for _, big := range []bool{false, true} {
			cs := []uint64{1<<32 - 1, 1<<32 - 2}
			os := []uint64{1<<32 - 1, 1<<32 - 8, 1 << 20}
			if big {
				cs = append(cs, 1<<63, 1<<63-1, 1<<64-1, 1<<64-2)
				os = append(os, 1<<63-8, 1<<63, 1<<64-8, 1<<64-1)
			}
			for _, c := range cs {
				for _, o := range os {
					t.Run(fmt.Sprintf("%d/%v/%x/%x", typ, big, c, o), func(t *testing.T) {
						for _, tag := range []uint16{274, 700} {
							p := page0(t, oobTIFF(big, tag, typ, c, o), tiff.Limits{})
							es := p.Orientations
							if tag == 700 {
								es = p.XMPs
							}
							if len(es) != 1 || !es[0].OutOfRange || es[0].Data != nil || es[0].Value != 0 ||
								!slices.Equal(p.Info().Skipped, []uint16{tag}) {
								t.Fatalf("tag %d %+v", tag, es)
							}
						}
					})
				}
			}
		}
	}
}

// Orientation entries list in every tag order, whatever the last entry is.
func TestOrientationEntriesAnyOrder(t *testing.T) {
	bad := [][4]uint64{{274, 12, 1, 0}, {274, 8, 1, 0}, {274, 2, 1, 0}, {274, 3, 2, 0}, {274, 3, 0, 0}}
	for _, big := range []bool{false, true} {
		for _, x := range bad {
			for _, first := range []bool{false, true} {
				b := oobTIFF(big, 274, 3, 1, 6, x)
				if first {
					b = oobTIFF(big, uint16(x[0]), uint16(x[1]), x[2], x[3], [4]uint64{274, 3, 1, 6})
				}
				p := page0(t, b, tiff.Limits{})
				if len(p.Orientations) != 2 {
					t.Fatalf("big=%v %v first=%v: %+v", big, x, first, p.Orientations)
				}
			}
		}
		if p := page0(t, oobTIFF(big, 274, 8, 1, 6), tiff.Limits{}); len(p.Orientations) != 1 || !p.Orientations[0].BadType {
			t.Fatalf("lone sshort: %+v", p.Orientations)
		}
	}
}
