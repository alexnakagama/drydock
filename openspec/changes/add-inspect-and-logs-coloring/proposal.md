# Proposal

## Why

The inspect and logs views take the whole screen and are where users read the most text, but they are the only views drydock draws with no color at all. Only their empty-state messages are styled. A long inspection document is hard to scan for keys and values, and in a busy log the errors look the same as every other line. The list views already show state through color, and the palette already has the colors this needs, in light and dark versions.

## What Changes

- **Colored inspection JSON.** Object keys, string values, numbers, literals (`true`, `false`, `null`) and punctuation each get their own palette style in every inspect view (containers, images, volumes, networks). In a container's inspection, the `State.Status` value takes the same color the container's row has in the list (running green, paused or restarting orange, exited, created or dead dimmed).
- **Colored log lines.** A timestamp at the start of a line is dimmed. The first recognized log-level token within the first 512 bytes of a line is colored: `ERROR`/`FATAL`/`PANIC`/`CRIT` in the error color, `WARN` in the warning color, `DEBUG`/`TRACE` dimmed. Uppercase level words are recognized as whole words anywhere in that window. Lowercase ones are recognized only in structured forms such as `level=error`, `"level":"error"` or `[error]`, so ordinary prose like "no error" is not colored.
- **Highlighted search matches in logs.** While a log search is active, every occurrence of the search text on the shown lines gets a highlight background, and it is also underlined so it stays visible without color. Search still filters the lines as it does now.
- The text on screen does not change: coloring only adds styling. Horizontal scrolling, line widths, truncation and every golden snapshot (which strip styling) stay the same.
- Coloring follows the light/dark palette and respects `NO_COLOR`, like the rest of the UI.

No key bindings change, and nothing is a breaking change.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `tui-navigation`: adds requirements for syntax-colored inspection JSON, colored log timestamps and levels, and highlighted log search matches. This capability already owns the UI's color requirements (state-colored rows, palette adapting to the terminal background). The logs search scenario in `container-management` keeps its filtering behavior unchanged.

## Impact

- `internal/ui/components/styles.go`: new palette entries and styles for JSON tokens, log timestamp and level styles, and the search-match highlight, in both palettes.
- `internal/ui/components`: exported `Span`/`SpanKind` types and `RenderSpans`, shared by the scanners in `views`.
- `internal/ui/components/pager.go`: rendering of styled lines: styling is applied before the horizontal cut, and widths come from the plain text, so scrolling is unaffected.
- `internal/ui/views/inspect.go`: a line-based JSON token scanner over the already-indented output, plus the `State.Status` tone.
- `internal/ui/views/logs.go`: detection of timestamps, levels and search matches for the visible lines only.
- Tests: new unit tests for the scanners and for styled rendering. Existing golden files are unchanged.
- No new dependencies and no engine API changes.
