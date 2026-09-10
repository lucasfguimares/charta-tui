# Architecture

Charta is a Go application built around Bubble Tea's message/update/view loop. Database I/O and SQL analysis run as commands so terminal rendering remains responsive.

```text
cmd/charta
    └── internal/cli          composition, paths and process lifecycle
            └── internal/tui  state, messages, input routing and views
                 ├── internal/sqleditor     lexer, diagnostics, formatting and completion
                 ├── internal/database      pools, catalog inspection and query execution
                 ├── internal/profile       profiles and secret storage
                 ├── internal/queryhistory  bounded local history
                 ├── internal/querylibrary  favorites and snippets
                 └── internal/activity      redacted rotating logs
```

## Runtime flow

1. `internal/cli` resolves local paths, creates the stores and database services, then starts Bubble Tea.
2. The TUI owns screen and interaction state. It starts asynchronous commands for database operations, persistence and debounced SQL analysis.
3. Each query tab owns its document version, editor state, analysis, diagnostics, selection, result table and cancellation function.
4. SQL analysis returns a versioned result. The document rejects stale results produced before a newer edit.
5. Query execution resolves statements with the same dialect-aware boundary implementation used by the editor.
6. Driver errors are normalized to a common location/message structure before the TUI creates an editor diagnostic.

## SQL editor boundaries

`internal/sqleditor` does not depend on Bubble Tea or a database driver. Its main abstractions are:

- `Dialect`: vocabulary and supported syntax features.
- `Lexer`: lossless tokens with source ranges.
- `DiagnosticProvider`: structural and dialect-specific diagnostics.
- `Formatter`: token-preserving whitespace and keyword formatting.
- `Document`: text/version state and stale-analysis rejection.
- Statement resolution and delimiter matching utilities.

The built-in parser is a fast structural validator rather than a complete SQL grammar. Server-side validation remains authoritative.

## Persistence and security

Profile JSON never contains passwords. `SecretStore` isolates OS-keyring access and allows an in-memory fallback. Profile, history and library files use owner-only permissions and atomic replacement. Activity logging redacts sensitive values and excludes SQL text and results.

## Extension points

- Add a database engine by implementing profile validation, manager DSN creation, catalog queries, error normalization and dialect selection.
- Add deeper syntax validation through another `DiagnosticProvider` without coupling it to rendering.
- Add editor settings through `sqleditor.FormatOptions`; persistence should remain outside the editor package.
