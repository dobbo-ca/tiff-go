# Building

Requires Go 1.27.1+ and read access to `dobbo-ca/deflate-go` and
`dobbo-ca/jpeg-go` (private):

```
export GOPRIVATE=github.com/dobbo-ca/deflate-go,github.com/dobbo-ca/jpeg-go,github.com/dobbo-ca/jp2k-go,github.com/dobbo-ca/webp-go
gh auth setup-git
make test
```

`make check` runs gofmt, vet (amd64 and arm64), golangci-lint and the race
tests. `make fuzz` runs `FuzzTIFF` for 30 seconds. CI mints a read-only GitHub
App token for the copies per job (`.github/workflows/ci.yml`); fork PRs get no
token, so their copy-dependent jobs skip.
