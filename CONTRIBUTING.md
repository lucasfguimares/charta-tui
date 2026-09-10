# Contributing to Charta

Thanks for helping improve Charta. Small, focused changes with a reproducible reason are easier to review and maintain.

## Before opening an issue

- Search existing issues and discussions.
- Confirm the behavior on the latest `main` branch or release.
- Remove passwords, connection strings, query results and proprietary SQL from examples.
- Use a minimal SQLite reproduction when the problem is not database-specific.

Security vulnerabilities must follow [SECURITY.md](SECURITY.md), not the public issue tracker.

## Development setup

Charta requires Go 1.26 or newer.

```sh
git clone https://github.com/lucasfguimares/charta-tui.git
cd charta-tui
go mod download
go test ./...
go run ./cmd/charta
```

Run the local quality checks before submitting a pull request:

```sh
make fmt-check
make vet
make test
make lint
```

`make test-race`, `make security` and `make release-check` need their corresponding native tools. Network integration tests are opt-in and must use disposable databases.

## Change guidelines

- Keep pull requests focused on one problem.
- Add a failing test before or with a behavior fix.
- Preserve SQL text unless the feature explicitly formats or qualifies it.
- Treat PostgreSQL, T-SQL and SQLite syntax independently.
- Keep database calls and expensive analysis outside the synchronous render path.
- Update user documentation when commands, storage, security behavior or shortcuts change.
- Do not add dependencies when the standard library or an existing dependency is sufficient.

Use Conventional Commit-style subjects such as `fix(editor): preserve cursor after formatting`. Maintainers may squash a pull request when its intermediate commits do not provide useful history.

## Tool-assisted contributions

Development tools, including code-completion and generative systems, are allowed. The contributor remains responsible for every submitted line:

- understand the implementation and explain its tradeoffs during review;
- verify licenses and provenance of copied or generated material;
- add tests that exercise the actual behavior;
- remove fabricated APIs, repetitive comments and unsupported claims;
- never include prompts, credentials or private source material.

Tool output is not evidence that a change is correct. Reviewers assess the code, tests and reasoning in the same way regardless of how it was drafted.

## Pull requests

Complete the pull-request template, link the issue when one exists and include terminal screenshots for visible TUI changes. A pull request is ready when required CI checks pass, the diff contains no unrelated formatting churn and documentation matches the behavior.

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
