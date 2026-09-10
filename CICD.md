# CI/CD reference

Charta uses GitHub Actions for pull-request validation, security analysis and tagged releases. Workflow dependencies are pinned to immutable commit SHAs and updated by Dependabot.

## Required pull-request checks

| Workflow | Checks |
| --- | --- |
| CI | Tests on Linux, macOS and Windows; module verification; tidy check; `go vet`; race detector; coverage floor; smoke build |
| Lint | `golangci-lint` formatting and configured linters |
| Security | `govulncheck`, CodeQL and dependency review |

Maintainers should require the successful CI, lint and security checks before merging into `main`. Network integration tests remain opt-in because they need disposable PostgreSQL or SQL Server instances.

## Local parity

```sh
make fmt-check
make vet
make test
make test-race
make lint
make security
make release-check
```

`make ci` combines the same checks. Race detection requires a Go environment with CGO enabled.

## Release process

1. Update `CHANGELOG.md` and confirm all required checks pass on `main`.
2. Run `make release-check` to validate a snapshot locally.
3. Create an annotated semantic-version tag such as `v0.1.0`.
4. Push the tag. The release workflow tests the source, builds all supported OS/architecture pairs and publishes the GitHub release.
5. Mark v0.1.0 as a pre-release while the project is in beta.

```sh
git tag -a v0.1.0 -m "charta v0.1.0"
git push origin v0.1.0
```

GoReleaser publishes compressed binaries, `checksums.txt` and archive SBOMs. GitHub Actions also creates build-provenance attestations for the checksum subject.

Verify a downloaded artifact with its checksum and, when the GitHub CLI is available, its attestation:

```sh
gh attestation verify charta_0.1.0_linux_amd64.tar.gz \
  --repo lucasfguimares/charta-tui
```

Do not publish releases from an unreviewed working tree or move a published version tag.
