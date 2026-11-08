# Agent notes

- Layout: `tiff.go` facade; `internal/{e,ifd,codec,decode,fixture}`; see `docs/design.md`.
- Tests: `make test`; `make check` before a PR; `make fuzz` after touching a parser.
- Pure Go, no cgo. Never echo file content or names into errors or logs.
- Deflate and JPEG come from private copies: set `GOPRIVATE` (docs/development/building.md).
- Issues are tracked with `bd` (beads) when `.beads/` is present.
