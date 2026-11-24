# Proposal

## Why

drydock's features are hard to find and its lists are hard to read at a glance. The images and volumes views can only be reached through the `:` command bar. Every row looks the same whatever the resource's state. The footer's key hints get cut off on an 80-column terminal. Nothing shows how many rows a list has or where the selection is. A new user has to open `?` or the README just to learn that the other views exist.

## What Changes

- **Resource tabs**: a new line below the header shows `containers`, `images` and `volumes`, each with its count. It highlights the resource the current stack belongs to. `1`/`2`/`3` jump to a resource, and `Tab`/`Shift+Tab` cycle through them. Switching replaces the view stack, as the command bar does. Pressing the key of the resource already open at the root does nothing, so no filter is lost. The command bar keeps working. The content area loses one line to the new strip on terminals of 5 or more lines.
- **State-colored rows**: rows take a color from their resource's state. Running containers are green. Paused and restarting containers are orange. Exited, created and dead containers are dimmed. Dangling images and unused volumes are also dimmed. The selected row stays reverse-video.
- **Footer that fits**: the footer shows the view's keys in priority order. When they don't fit the width, it drops the lowest-priority hints and ends with `? more`. Hints for actions that don't apply to the selected row are hidden: for example `x shell` on a stopped container, or `enter containers` on an image no container uses. `s` is labeled `start` or `stop` to match the selected container. The help overlay still lists every key, and dims the ones that don't apply right now.
- **List position**: list views show `<selected>/<shown>`, for example `3/12`. With a filter applied it reads `3/5 of 12`.

There are no breaking changes. Existing key bindings keep their meaning. `1`, `2`, `3`, `Tab` and `Shift+Tab` are new global keys, and none of them was bound before.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tui-navigation`:
  - The full-screen layout gains the resource tab strip.
  - New requirements cover switching resources with tabs, state-colored rows, context-aware footer hints that fit the width, and the list position indicator.
  - The help overlay requirement changes to show inapplicable keys dimmed.

## Impact

- `internal/ui/render.go`: tab strip, position indicator, footer fitting, help overlay dimming, and `chromeLines` going from 3 to 4.
- `internal/ui/model.go`: tracks the active root resource, and handles the new global keys.
- `internal/ui/keys/keys.go`: new global bindings.
- `internal/ui/components/table.go`: a per-row style, and position accessors.
- `internal/ui/components/styles.go`: row state styles.
- `internal/ui/views/{containers,images,volumes}.go`: row tones, context-aware `KeyHelp`, and priority ordering.
- Tests in `internal/ui` and `internal/ui/views`, and the key tables in `README.md`.
- `internal/store/snapshot.go`: adds the non-cloning `NumContainers`/`NumImages`/`NumVolumes` count helpers. There are no other store changes and no Docker-layer changes.
- No new dependencies.
