// Package tiff decodes multi-page TIFF files under strict resource limits.
//
// Open walks the IFD chain (refusing loops and more than Limits.MaxIFDs
// directories), Page parses one directory (refusing more than
// Limits.MaxPixels), and Rows delivers the pixels as bands of full-width
// rows, top to bottom. A strip or tile of any size is decoded incrementally,
// so no band exceeds Limits.MaxBandBytes; only a tile row (all tiles of one
// row) is buffered whole, and a row of tiles larger than the cap is refused.
//
// Samples are delivered packed as stored, rows padded to a byte, 16-bit
// samples big-endian, predictor undone. JPEG-compressed data comes back as
// 8-bit grey, RGB (YCbCr converted) or four samples as stored (CMYK). The Photometric in Info says
// how to read the samples; ColorMap carries a palette.
//
// Supported: classic and BigTIFF, either byte order; none, PackBits, LZW,
// Deflate, CCITT G3/G4 and JPEG compression; predictor 2 at 8 and 16 bits;
// 1 to 16 bits per sample; grey, RGB, palette and CMYK. Everything else
// returns an error wrapping ErrUnsupported.
package tiff
