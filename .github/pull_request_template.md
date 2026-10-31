## Summary

<!-- What changed and why. -->

## Checklist

- [ ] `package.json` version bumped
- [ ] Matching entry added or updated in `release-notes/releases.json` (see the top of AGENTS.md)
- [ ] `go run ./cmd/releasenotes` run and `CHANGELOG.md` committed
- [ ] `go test ./internal/releasenotes` passes
- [ ] `gofmt` clean and targeted tests pass
