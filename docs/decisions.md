# Decisions

1. **Own repo, public, MIT.** `x/image/tiff` reads only the first IFD and
   allocates the whole image from header dimensions.
2. **Deflate and JPEG through the private copies** `dobbo-ca/deflate-go`
   (`zlib.NewReader`, streaming) and `dobbo-ca/jpeg-go` (`Region` decode).
   Neither copy has a tag under the `dobbo-ca` module path yet, so `go.mod`
   pins pseudo-versions of their `main`. Tag both and re-pin before v0.1.0.
3. **CCITT through `golang.org/x/image/ccitt`**, which streams row by row.
   WhiteIsZero maps to its `Invert` option so samples follow Photometric.
4. **LZW is written here**: TIFF's MSB-first, early-change variant is not what
   `compress/lzw` decodes. Old-style (LSB, pre-6.0) LZW is refused.
5. **Zero `Limits` is safe.** Defaults: 10 000 IFDs, 178 956 970 pixels per IFD
   (kleio's upload ceiling), 64 MiB per band.
6. **Strips never hit the band cap**: a strip is read a band of rows at a time.
7. **Tiles buffer one tile row** (full image width by tile height) and refuse
   it above the band cap (`LimitError{Name: "band bytes"}`). A tile row over
   64 MiB needs a tile 64 MiB tall or an image millions of pixels wide, which
   no real file has. Lifting it means decoding the row's tiles in lockstep.
8. **Refused as `ErrUnsupported`**: planar configuration 2, fill order 2, mixed
   bits per sample, sample formats other than unsigned integer, predictor 3,
   predictor 2 outside 8 and 16 bits, Lab/YCbCr outside JPEG, old-style JPEG
   (compression 6), JPEG with 4 components that is not baseline, one interleaved
   scan, 1x1 sampling (libtiff refuses the sampling too), CCITT with more than 1 bit per
   pixel, compression 2 (CCITT RLE).
9. **JPEG is untested with a `JPEGTables` tag**: jpeg-go has no abbreviated
   stream writer, so the fixture writes self-contained streams. The tables
   path is a straight pass-through to `jpeg.DecodeConfig` and `DecodeOptions`.
10. **ctx** is checked before every band (and tile); a single band is not
    interruptible.
11. **Four-component JPEG is decoded here, not by jpeg-go**, which stops at three
    components. `internal/codec/jpeg4.go` reads a baseline interleaved scan (Huffman,
    libjpeg's jidctint.c IDCT) and returns the samples as stored: libtiff gives libjpeg
    no colour space for a Separated page, so an Adobe marker's transform is ignored and
    there is no inversion. Pillow's reading of the testdata files matches byte for byte.
    The Huffman decode is bit by bit, which is slow on very large pages.
