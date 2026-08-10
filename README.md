# tui-db

`tui-db` is a keyboard-driven SQL workbench for the terminal. It keeps connection management, schema exploration, query editing, results, and local activity logs in one TUI.

## Features

- PostgreSQL, Microsoft SQL Server, and SQLite connections
- Saved connection profiles with passwords kept out of the profile file
- Per-connection default schema for unqualified SQL interactions
- OS keyring integration, with an in-memory fallback when a keyring is unavailable
- Browsable schemas, tables, views, and columns
- Multiple query tabs with per-tab cancellation
- Statement-at-cursor and marked-selection execution
- Dialect-aware SQL highlighting, diagnostics, delimiter matching, and formatting
- Context-aware SQL autocomplete backed by parsed query scopes and cached connection metadata
- Debounced background validation with inline error markers and F8 navigation
- Full-script execution and driver error locations mapped back into the editor
- Virtualized result grid with fixed headers, active cells, and horizontal scrolling
- PK/FK result markers backed by database result-set and catalog metadata
- Typed cell formatting, full-value details, search, row jump, and clipboard actions
- Confirmation before potentially mutating SQL runs
- Configurable query timeout and result cap per connection
- Rotating, redacted JSON activity logs that never record SQL text or passwords
- TLS modes and certificate settings for network databases

## Requirements

- Go 1.26 or newer to build from source
- A terminal at least 80 columns by 24 rows
- An OS keyring service for persistent passwords (Secret Service on Linux, Keychain on macOS, or Credential Manager on Windows)

SQLite support is embedded and does not require a system SQLite installation.

## Build and run

```sh
make build
./bin/tui-db
```

For a session that must not access the OS keyring:

```sh
./bin/tui-db --no-keyring
```

Use `tui-db --help` for all flags and `tui-db version` for build metadata. A custom profile file can be selected with `--config PATH`.

## First connection

Press `a` in the connection browser, select the database engine with `Ctrl+D`, complete the form, and save with `Ctrl+S`. Passwords are requested when needed and are never written to the connection profile JSON.

The optional **Default schema** field controls resolution of unqualified relation names. New PostgreSQL, SQL Server, and SQLite profiles start with `public`, `dbo`, and `main`, respectively. PostgreSQL applies it as the first entry in `search_path`; SQL Server and SQLite safely qualify otherwise-unqualified relations at execution time. Explicitly qualified names and CTE references are left unchanged. The schema is validated when the catalog is loaded and automatically expanded in the browser.

Network connections default to certificate and hostname verification. For development databases without TLS, explicitly select the `disable` TLS mode in the profile. SQLite profiles accept a local database path and can be opened read-only.

## Keyboard map

| Area | Key | Action |
| --- | --- | --- |
| Global | `F1` | Open help |
| Global | `Ctrl+Q` | Quit, with confirmation for open editors |
| Global | `Ctrl+L` | Open activity logs |
| Workspace | `Tab` / `Shift+Tab` | Cycle browser, editor, and results focus |
| Workspace | `Ctrl+B` | Toggle the connection browser |
| Tabs | `Ctrl+T` | Open a query tab for the selected connection |
| Tabs | `Ctrl+W` | Close the active tab |
| Tabs | `Ctrl+PgUp` / `Ctrl+PgDn` | Switch tabs |
| Query | `Ctrl+Enter` | Run the marked selection or statement at the cursor |
| Query | `Ctrl+Shift+Enter` | Run the complete script |
| Query | `Ctrl+Shift+F` | Format the marked selection or current statement |
| Query | `F8` / `Shift+F8` | Move to the next or previous diagnostic |
| Query | `Ctrl+Space` | Open contextual SQL autocomplete |
| Query | `Up` / `Down` | Navigate autocomplete suggestions |
| Query | `Enter` / `Tab` | Accept the selected autocomplete suggestion |
| Query | `Esc` | Close autocomplete |
| Query | `Ctrl+Shift+Space` | Start or clear a selection at the cursor |
| Query | `Ctrl+C` / `Ctrl+G` | Cancel the active query |
| Results | arrows / `WASD` | Move the active cell |
| Results | `PageUp` / `PageDown` | Move one result page |
| Results | `Home` / `End` | Move to the first or last column |
| Results | `Ctrl+Home` / `Ctrl+End` | Move to the first or last result |
| Results | `Enter` | Open the complete cell value |
| Results | `c` / `Shift+C` | Copy the active cell or row |
| Results | `r` / `f` / `g` | Rerun, find a value, or go to a row |
| Browser | `Enter` / arrow keys | Connect, expand, collapse, and navigate |
| Browser | `a` / `e` / `c` / `d` | Add, edit, copy, or delete a profile |
| Browser | `t` / `x` / `r` | Test, disconnect, or refresh a connection |

A marked selection must contain exactly one SQL statement. Without a selection, the statement containing the cursor is run. Potentially mutating statements require confirmation; `!` in that confirmation trusts writes for the current tab only.

The editor selects its SQL dialect from the active connection (T-SQL, PostgreSQL, or SQLite); the editor core also defines MySQL/MariaDB vocabulary for future connection support. Formatting defaults to four spaces, uppercase keywords, trailing commas, and line breaks for columns, joins, and boolean expressions. These choices are represented by `sqleditor.FormatOptions` so frontends or future persisted settings can change them without coupling formatting to rendering.

The result footer reports row and column counts, query/fetch/render timing, the active cell, viewport range, and the configured row limit. Result column width is configurable per connection and defaults to 40 terminal cells.

## Local data and security

The default paths follow the operating system's config and cache directories:

- Profiles: `<config>/tui-db/connections.json`
- Activity log: `<cache>/tui-db/activity.jsonl`

Profile files are created with owner-only permissions and updated atomically. Passwords are stored under the `tui-db` keyring service or retained only in memory when persistence is unavailable. Query errors and activity fields are redacted before display or storage. The log rotates at 1 MiB and retains five backups.

The SQL safety check is deliberately conservative, but it is not a database permission boundary. Use least-privilege database accounts for real protection.

## Development

```sh
make fmt
make vet
make test
make test-race
make lint
```

Network integration tests are opt-in. Set the relevant variables, then run `make test-integration`:

```text
TUIDB_TEST_POSTGRES_HOST
TUIDB_TEST_POSTGRES_PORT
TUIDB_TEST_POSTGRES_DATABASE
TUIDB_TEST_POSTGRES_USERNAME
TUIDB_TEST_POSTGRES_PASSWORD

TUIDB_TEST_SQLSERVER_HOST
TUIDB_TEST_SQLSERVER_PORT
TUIDB_TEST_SQLSERVER_DATABASE
TUIDB_TEST_SQLSERVER_USERNAME
TUIDB_TEST_SQLSERVER_PASSWORD
```

If a host variable is absent, that integration test is skipped. Integration tests disable TLS so they can target disposable local containers; application profiles still use secure TLS defaults.

## License

MIT — see [LICENSE](LICENSE).
