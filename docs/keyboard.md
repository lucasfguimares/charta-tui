# Keyboard reference

## Global and workspace

| Key | Action |
| --- | --- |
| `F1` or `?` | Open help |
| `Ctrl+Q` | Quit, confirming when editors are open |
| `Ctrl+L` | Open activity logs |
| `Ctrl+H` | Open query history |
| `Ctrl+Shift+P` | Open favorites and snippets |
| `Tab` / `Shift+Tab` | Cycle browser, editor and results focus |
| `Ctrl+B` | Toggle the schema browser |
| `Ctrl+T` | Open a tab for the selected connection |
| `Ctrl+W` | Close the active tab |
| `Ctrl+PgUp` / `Ctrl+PgDn` | Switch tabs |

## Query editor

| Key | Action |
| --- | --- |
| `Ctrl+Enter` | Run the selection or statement at the cursor |
| `Ctrl+Shift+Enter` | Run the complete script |
| `Ctrl+Shift+F` | Format the selection or current statement |
| `Ctrl+Shift+Space` | Start or clear a selection |
| `Ctrl+Space` | Open SQL autocomplete |
| `Up` / `Down` | Navigate autocomplete suggestions |
| `Enter` | Accept the selected suggestion |
| `Space` | Accept ghost text, or insert a space |
| `Tab` / `Shift+Tab` | Expand a snippet or move between placeholders |
| `Esc` | Dismiss autocomplete |
| `F8` / `Shift+F8` | Next or previous diagnostic |
| `Ctrl+S` | Save the query as a favorite |
| `Ctrl+C` / `Ctrl+G` | Cancel the active query |

## Results

| Key | Action |
| --- | --- |
| Arrows or `WASD` | Move the active cell |
| `PageUp` / `PageDown` | Move one result page |
| `Home` / `End` | First or last column |
| `Ctrl+Home` / `Ctrl+End` | First or last result |
| `Enter` | Open the complete cell value |
| `v` | Open the selected row vertically |
| `c` / `Shift+C` | Copy the active cell or row |
| `r` / `f` / `g` | Rerun, find a value or go to a row |
| `Up` from first row | Select the column header |

When a column header is selected, use `Left` / `Right` to move, `Enter` to inspect metadata, `Space` to freeze columns, `u` to clear frozen columns and `Down` to return to rows.

## Inspectors

| Key | Action |
| --- | --- |
| `Up` / `Down` or page keys | Navigate or scroll |
| `Left` / `Right` | Previous or next column in the column inspector |
| `s` | Load exact column statistics |
| `r` | Refresh metadata and loaded statistics |
| `c` / `Shift+C` | Copy an identifier, value or row field |
| `Enter` | Expand or collapse a long row value |
| `Esc` | Return to results |

Exact statistics are opt-in because `COUNT(DISTINCT ...)` may scan the source relation.

## Connections

| Key | Action |
| --- | --- |
| `Enter` or `Right` | Connect or expand |
| `Left` | Collapse |
| `a` / `e` / `c` / `d` | Add, edit, copy or delete a profile |
| `t` / `x` / `r` | Test, disconnect or refresh |

Connection and favorite forms use `Ctrl+S` to save, `Tab` / `Shift+Tab` to move between fields and `Esc` to cancel.

## History and library

History and library lists use arrows or `j` / `k` to navigate, `/` to search, `Enter` to open, `Ctrl+Enter` to open and run, and `Delete` to remove an item. In history, `c`, `s` and `p` cycle connection, status and period filters.
