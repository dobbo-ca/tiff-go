package tiff_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	tiff "github.com/dobbo-ca/tiff-go"
)

type tag struct {
	id, typ uint16
	v       uint32
}

// raw is a little-endian classic TIFF: header, a data blob, then one IFD.
func raw(data []byte, tags []tag) []byte {
	b := append([]byte("II*\x00\x00\x00\x00\x00"), data...)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)))
	b = binary.LittleEndian.AppendUint16(b, uint16(len(tags)))
	for _, t := range tags {
		b = binary.LittleEndian.AppendUint16(b, t.id)
		b = binary.LittleEndian.AppendUint16(b, t.typ)
		b = binary.LittleEndian.AppendUint32(b, 1)
		b = binary.LittleEndian.AppendUint32(b, t.v)
	}
	return binary.LittleEndian.AppendUint32(b, 0)
}

func TestHostileHeaders(t *testing.T) {
	lim := tiff.Limits{MaxIFDs: 64, MaxPixels: 1 << 22, MaxBandBytes: 1 << 20}
	for name, tc := range map[string]struct {
		tags []tag
		want error
	}{
		"rows per strip 0":       {[]tag{{256, 4, 4}, {257, 4, 4}, {262, 3, 1}, {273, 4, 8}, {278, 4, 0}, {279, 4, 16}}, tiff.ErrCorrupt},
		"tile wider than a band": {[]tag{{256, 4, 1}, {257, 4, 1}, {262, 3, 2}, {277, 3, 3}, {322, 4, 1 << 28}, {323, 4, 1}, {324, 4, 8}, {325, 4, 16}}, tiff.ErrLimit},
	} {
		t.Run(name, func(t *testing.T) {
			b := raw(make([]byte, 16), tc.tags)
			_, _, err := decode(t, b, lim, 0)
			if !errors.Is(err, tc.want) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestOracleFiles(t *testing.T) {
	read := func(n string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", "golden", n))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	truth := read("bw.tif")[8 : 8+4082] // tiffcp from this uncompressed file made the others
	for _, n := range []string{"bw_g4_r16.tif", "bwmw_g4_r16.tif"} {
		b := read(n)
		f, err := tiff.Open(bytes.NewReader(b), int64(len(b)), tiff.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		p, err := f.Page(0)
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		if err := p.Rows(context.Background(), func(_ int, r []byte) error { got = append(got, r...); return nil }); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		for y := 0; y < 157; y++ { // ignore the pad bits of each row's last byte
			for x := 0; x < 26; x++ {
				m := byte(0xff)
				if x == 25 {
					m = 0xe0
				}
				if i := y*26 + x; got[i]&m != truth[i]&m {
					t.Fatalf("%s: differs from libtiff at row %d byte %d", n, y, x)
				}
			}
		}
	}
	for _, n := range []string{"bw_g3pfill_r16.tif", "bw_g3p2d_r16.tif"} {
		if _, _, err := decode(t, read(n), tiff.Limits{}, 0); !errors.Is(err, tiff.ErrUnsupported) {
			t.Fatalf("%s: %v", n, err)
		}
	}
}

func TestFillOrder2At8Bits(t *testing.T) {
	b := raw([]byte{7, 0, 0, 0}, []tag{{256, 4, 1}, {257, 4, 1}, {258, 3, 8}, {262, 3, 1}, {266, 3, 2}, {273, 4, 8}, {278, 4, 1}, {279, 4, 1}})
	if got, _, err := decode(t, b, tiff.Limits{}, 0); err != nil || !bytes.Equal(got, []byte{7}) {
		t.Fatalf("%v %v", got, err)
	}
}
