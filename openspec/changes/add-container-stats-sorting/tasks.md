# Tasks

## 1. Sort setting and key binding

- [ ] 1.1 Add the `sortOrder` type (`sortName` zero value, `sortCPU`, `sortMem`) and a `Sort` field to `views.Prefs` in `internal/ui/views/resources.go`, documenting the invariant that a sort other than name implies `Stats`; verify `go build ./...` succeeds.
- [ ] 1.2 Add `Sort` (`o`, help `sort cpu`) to `keys.Containers` in `internal/ui/keys/keys.go` and to its help list; extend `keys_test.go` with `{"Sort", Containers.Sort, "o"}` and verify `go test ./internal/ui/keys/` passes, including the existing check that no two containers bindings share a key.

## 2. Comparator

- [ ] 2.1 Extract the "has a value" test from `statsCells` into a helper used by both `statsCells` and the new comparator, and verify the existing stats tests in `containers_stats_test.go` still pass unchanged.
- [ ] 2.2 Implement the comparator from design.md (has value first; CPU as the tenths shown in the cell, taken from the same formatting as `formatCPU` and not from `math.Round`, or MEM bytes, descending; then name; then ID) as a plain function, with table-driven unit tests covering: largest first for CPU and MEM, `0.04%` vs `0.0%` comparing equal and falling back to name, a half such as `0.25` ordering the same as the cell text `0.2%`, rows equal in every key staying in a deterministic order (name, then ID), exited/paused/no-sample/`HasMem=false` rows last in name order, and the disconnected state putting every row in name order; verify `go test ./internal/ui/views/ -run Sort` passes.

## 3. View behavior

- [ ] 3.1 Sort `v.list` in `setRows()` by the effective order (`prefs.Sort` when `statsCols`, otherwise name) and record `sortApplied`; add a test that sends a stats batch in which `worker` overtakes `api` and verify the row order flips while the selected container stays selected (spec "Live re-sorting").
- [ ] 3.2 Handle `o` in `containersView.Update` with a `cycleSort` that advances name → CPU → MEM → name and, when it leaves name with stats off, turns stats on through the existing `setColumns`/`syncStats` path; add tests for each step of the cycle, including that pressing `o` with stats off starts the stats subscription and that MEM → name leaves stats on.
- [ ] 3.3 Make `toggleStats` reset `prefs.Sort` to name when it turns stats off; add a test for spec scenario "Turn stats off while sorted by CPU" (name order, no sort indicator, footer `o sort cpu`).
- [ ] 3.4 Make `SetActive(true)` call `setRows()` when the layout matches but `sortApplied != prefs.Sort`; add tests for spec "Stats sort applies to every containers view": a drill-down from an image keeps the CPU sort, a sort changed in a drill-down shows in the root view after Esc, `:containers` reopens sorted by MEM, and a new `Resources` set starts in name order with stats off.
- [ ] 3.5 Append `sort cpu`/`sort mem` to `Indicators()` after `stats` and before the filter indicator, and append the relabelled `o` binding to `KeyHelp()` after `t`; add tests for both header scenarios and the footer label for each order, extend `keyhelp_test.go` so help lists `o`, update the existing exact-list key-help expectations in `keyhelp_test.go` for containers (they gain `o`) and the existing `TestFooterDropsStatsHintFirst` (now `o` is dropped before `t`), and add a narrow-footer test showing `o` is dropped before `t`.
- [ ] 3.6 Run `go test ./internal/ui/views/ ./internal/ui/ -update` (both packages define the flag), review the golden diff (`git diff internal/ui`) and confirm only footer lines changed, then add a golden snapshot of a CPU-sorted stats layout (for example `containers_stats_sort_cpu_100x10.golden`) and verify `go test ./internal/ui/...` passes without `-update`.

## 4. Documentation

- [ ] 4.1 Update `README.md`: add `o` sort to the Containers row of the key table, and add to the stats paragraph that `o` cycles name → CPU → MEM (largest first, `-` rows last), turns stats on when needed, re-sorts as values refresh, applies session-wide, and that `t` turning stats off brings back the name order; on narrow terminals `o` is dropped before `t`. Verify the described keys match `keys.Containers`.

## 5. Integration checks

- [ ] 5.1 Run `go vet ./...` and `go test ./...` (including the integration tests in the environment CI uses) and verify everything passes.
- [ ] 5.2 Run drydock against a real engine with a few containers, one of them busy (for example `docker run -d --name burn busybox sh -c 'while :; do :; done'`): press `o` and confirm stats turn on and `burn` moves to the top, `o` again sorts by MEM, `o` again returns to name order with stats on, `t` while sorted resets to name, the selection follows a moving row, and the header and footer labels match the spec.
- [ ] 5.3 Run `openspec validate add-container-stats-sorting --strict` and verify it passes.

## Workflow follow-up

- Archive the change with `/opsx:archive` after review, which syncs the delta into `openspec/specs/container-management/spec.md`.
