# Design

Stages, one package each under `internal/`:

| Package | Job |
|---|---|
| `e` | `ErrLimit`, `ErrCorrupt`, `ErrUnsupported`, `LimitError` |
| `ifd` | header, IFD chain walk (loop and count caps), tag parsing, validated chunk ranges |
| `codec` | one strip or tile to rows: none, PackBits, LZW, Deflate, CCITT, JPEG |
| `decode` | strips and tiles to bands; predictor; 16-bit byte order |
| `fixture` | synthetic TIFF writer for tests |

The root package (`tiff.go`) is a facade: `Limits`, `Open`, `File`, `Page`,
`Info`, `Rows`.

Rules the parser keeps:

- No allocation scales with a count read from the file. Strip and tile offset
  and byte-count arrays are read element by element, on demand; small value
  arrays (bits per sample, colour map, JPEG tables) are bounded first.
- Every offset and length is checked against the file size before it is read.
- Output size is fixed by the header (rows times row bytes), so a compressed
  stream cannot expand past it: decoders are asked for exactly that many rows.
- Messages carry tag ids and numbers only, never file content.

Output: packed samples as stored (rows padded to a byte), 16-bit samples
big-endian, predictor undone. JPEG output is 8-bit grey or RGB.

JPEG chunks: the compressed chunk is held whole (jpeg-go takes a slice) and
decoded a band of rows at a time through its `Region` option. Each band
re-decodes entropy data from the chunk start, so a chunk of N bands costs
O(N^2) entropy work. Fine at the default 64 MiB band (a few bands per chunk);
revisit with restart-marker offsets if a workload needs it.
