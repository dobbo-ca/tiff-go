package codec

import "testing"

func TestProgressive(t *testing.T) {
	soi, app := []byte{0xff, 0xd8}, []byte{0xff, 0xe0, 0, 4, 1, 2}
	for sof, want := range map[byte]bool{0xc0: false, 0xc2: true} {
		b := append(append(append([]byte{}, soi...), app...), 0xff, sof, 0, 8)
		if got := progressive(b); got != want {
			t.Fatalf("SOF %x: %v", sof, got)
		}
	}
}
