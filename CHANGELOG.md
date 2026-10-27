# Changelog

All notable changes to this project are documented in this file.

## [Unreleased]

### Added

- Editor session persistence: open tabs, their text, cursor, selection, order, and the active tab are restored on the next start from `<config>/tui-db/workspace.json`. Restoring never connects or prompts for passwords.
- `--no-session` flag to disable reading and writing the session file.
- `Ctrl+R` attaches a restored tab whose connection profile no longer exists to the selected connection.
- Single-owner session lock: additional concurrent instances do not save the session and show a status-bar warning.
- Unreadable or unknown-version session files are preserved as `workspace.json.corrupt-<timestamp>`.
- `Ctrl+O` collapses the SQL editor so results use the full panel height.
- Schema explorer (`Ctrl+Shift+E`, or `i` in the browser): full-screen schemas, tables/views, and columns panes with type, nullability, keys, defaults, and FK targets; incremental filter; `SELECT` insertion; name copying; and column statistics.
- Explorer global find (`f` / `Ctrl+F`): ranked, as-you-type search over every schema, table, view, and column, with kind filter and jump-to-object.
- Explorer relation details: indexes, approximate rows and size, and highlighted view definitions (`d` cycles columns / indexes / definition); index-leading columns are marked `IX`.

### Changed

- Result find (`f`) is now an inline, incremental find bar: results stay visible while typing, matches are highlighted, a match counter is shown, and `n`/`N` move between matches.
- `Ctrl+Q` asks for confirmation only while a query is running, or when tabs with text are not being saved.
