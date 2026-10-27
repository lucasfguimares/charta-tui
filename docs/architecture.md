# Architecture

`tui-db` is a single Go binary. `cmd/tui-db` calls `internal/cli`, which is the composition root: it resolves paths, builds every service, and injects them into the Bubble Tea model in `internal/tui`.

## Packages

| Package | Responsibility |
| --- | --- |
| `internal/cli` | Cobra command, flags, path resolution (`resolvePaths`), service wiring |
| `internal/tui` | Bubble Tea model: browser, tabs, editor, results, modals |
| `internal/profile` | Connection profiles and secret stores (keyring, memory, hybrid) |
| `internal/database` | Connection manager, catalog inspector, query runner |
| `internal/sqleditor` | Dialects, lexer/parser, versioned `Document`, analysis, completion, formatting |
| `internal/sqlscan` | Statement splitting and risky-SQL detection |
| `internal/activity` | Rotating, redacted activity log |
| `internal/queryhistory` | Sanitized execution history store |
| `internal/querylibrary` | Favorites and snippets store |
| `internal/workspace` | Editor session snapshot store |

The local stores (`queryhistory`, `querylibrary`, `workspace`) share one pattern: a versioned JSON document, a `sync.RWMutex`, owner-only permissions, and atomic replacement through `os.CreateTemp` + `Sync` + `os.Rename`. They do not depend on Bubble Tea or database drivers. All store I/O from the TUI runs inside `tea.Cmd`s so rendering never blocks.

## Schema explorer (`internal/tui/explorer.go`)

The explorer is a full-screen mode over the same per-profile `catalogState` cache the browser tree and autocomplete use, so it issues no catalog query for data that is already loaded. Moving the cursor schedules an `explorerLoadDueMsg` (~150 ms) carrying a version; stale versions are dropped, so holding a key does not fire one query per row. `explorerLoadCmd` requests only the first missing level under the selection, and an in-flight map prevents duplicate requests.

Catalog messages carry `isExplorer`. Explorer-origin results fill the cache but never set the browser's `expanded` flags, so exploring does not unfold the sidebar. Schemas are loaded with an explorer-specific command that checks for a stored secret first; a missing secret becomes a "Password required" state instead of a prompt.

`Inspector.RelationDetails` (`internal/database/details.go`) reads indexes, the view definition, and best-effort size estimates per driver; estimate failures caused by missing permissions only clear the estimates. The explorer requests details for the selected relation after its columns, caches them in `catalogState.details`, and drops them on refresh. The global find (`explorer_find.go`) searches an in-memory index built from `catalogState` and the full autocomplete catalog, which it starts loading in the background; it never queries the database while typing.

The column inspector is shared with result grids: `columnInspectorModel` records its connection, whether it came from the catalog (no loaded-result statistics), and the mode to return to on `Esc`.

## Editor session (`internal/workspace`)

The session file `workspace.json` sits next to the profile file. It holds the ordered tabs (profile ID, raw SQL, 1-based cursor, optional selection anchor) and the active tab index. It is a snapshot: nothing live is restored.

**Ownership.** `workspace.Open` takes a non-blocking exclusive advisory lock on `workspace.json.lock` (`flock` on Unix, `LockFileEx` on Windows). A separate lock file is required because the atomic rename replaces the inode of `workspace.json`. The lock lives for the whole process and the OS releases it if the process dies. A store that does not own the lock still loads, but `Save` returns `ErrNotOwner`.

**Recovery.** If the file cannot be decoded or has an unknown version, the owner renames it to `workspace.json.corrupt-<timestamp>` and returns an empty snapshot. If that rename fails, the store disables itself so the original file is never overwritten.

**Limits.** `Save` keeps at most `MaxTabs` tabs and truncates SQL longer than `MaxSQLBytes` at a UTF-8 boundary, and it reports both in a `SaveReport` instead of failing. Each save carries a monotonic sequence number, so a slower, older write can never replace a newer one.

**TUI flow.**

1. `Init` loads the profiles. The first successful `profilesLoadedMsg` triggers `loadSessionCmd`, so tabs are always resolved against the loaded profiles.
2. `applySession` recreates the tabs with `addTab(connection, text)` and restores the cursor and selection. It issues no connection command, so no password prompt appears at startup. `isTrusted` and all result/run state start from zero values. A tab whose profile is gone becomes a detached "no connection" tab that keeps its original profile ID and text; `Ctrl+R` attaches it to the selected profile.
3. No save happens until the restore has completed, so a failed profile load can never overwrite the session with an empty one.
4. `Update` wraps the message switch and then calls `sessionChangesCmd`:
   - **Structural changes** (tab order, connection association, active tab) trigger an immediate save.
   - **Text changes**, detected through each tab's `sqleditor.Document` version, schedule a `sessionSaveDueMsg` about 2 seconds later. When it arrives, it is dropped if the document version has moved on, the same way as `sqlAnalysisDueMsg`.
5. Quitting performs one final, synchronous save before `tea.Quit`. `Ctrl+Q` asks for confirmation only while a query runs, or when the session is not being persisted (`--no-session` or a non-owner instance) and tabs contain text.
