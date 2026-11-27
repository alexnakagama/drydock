# Tasks

## 1. Project index in the store

- [x] 1.1 Add the Compose label constants (`com.docker.compose.project`, `.service`, `.oneoff`, `.project.working_dir`) in `internal/store` and verify `go build ./...` succeeds
- [x] 1.2 Add `buildProjects` next to `buildIndexes` (project name to its containers, sorted by name then ID, and a sorted list of project names, ignoring missing and empty labels) and call it where the snapshot is built in `store.go`, leaving `buildIndexes` and its call sites unchanged; verify with unit tests in `snapshot_test.go` covering grouping, unlabeled containers and a project disappearing with its last container
- [x] 1.3 Add `Snapshot.Projects()` returning per-project summaries (name, service and one-off containers, running/total over service containers, state running/partial/exited, working dir from the first container in name order) and `Snapshot.NumProjects()`; verify with unit tests for paused-is-not-running, all-paused is partial, only-one-off gives `0/0` exited (not running), and differing working dirs
- [x] 1.4 Let `dockerfake` seed containers with Compose labels (a helper or documented `Labels` use in `AddContainer`) and verify a fake-backed store test sees the projects in its snapshot
- [x] 1.5 Add a per-container failure hook to `dockerfake` (for example `FailFor(m Method, id string, err error)`, checked where a call starts), since `SetError`/`FailNext` fail by method only and cannot choose among parallel calls; verify with a `fake_test.go` test that only the named container's call fails

## 2. Four resource tabs

- [x] 2.1 Extend `internal/ui/keys` to four tab keys (`tabKeys`, `switchKeys`, `Tab4`, `TabKeys()`, the `Tab1`..`Tab3` comment, help `1-4/tab`) and verify `keys_test.go` passes with the new key and that no view binds `4`
- [x] 2.2 Update the root model tests (`model_test.go`) for four resources: number key `4`, `Tab` from the third to the fourth resource, wrap from the fourth to the first, `Shift+Tab` from the first to the fourth, the hard-coded tab strip strings, the narrow-alias width arithmetic in `TestTabsNarrowUseAliases`, and the `1-3/tab` assertion; regenerate `root_100x30.golden`, and verify `go test ./internal/ui/` passes
- [x] 2.3 Once the projects resource is registered (task 3.2), update the `1-3/tab` assertions in `internal/ui/views/root_test.go` and the harness's loaded-tabs strings, regenerate every view golden file that shows the tab strip with `-update`, review that the diff changes only the tab strip line, and verify `go test ./internal/ui/views/` passes

## 3. Projects view

- [x] 3.1 Add `projectsView` in `internal/ui/views` with the PROJECT, RUNNING, STATE and WORKING DIR columns, state glyphs and row tones, `~` for the home directory, the `No Compose projects.` empty message, the filter on PROJECT and WORKING DIR, selection kept across snapshots and `ui.Positioner`; verify with unit tests and new golden files (`projects_100x10.golden`, `projects_empty_100x8.golden`)
- [x] 3.2 Register the view as the fourth entry of `views.Resources()` (name `projects`, alias `pr`, `Count: store.Snapshot.NumProjects`) and verify in `root_test.go` that `:projects`, `:pr` and `4` open it and that the tab reads `4 projects (N)`, including `(0)` with no projects
- [x] 3.3 Implement Enter: push `NewFilteredContainers` on the project label with crumbs `{project, "containers"}`, including one-off containers; verify with tests for the breadcrumb `drydock > projects > myapp > containers`, the project-scoped `P` confirmation list, and Esc returning with the project still selected

## 4. Project actions

- [x] 4.1 Add the bounded parallel runner (at most 8 at a time, every target attempted, outcome summarized as succeeded/failed with up to three failed names and the first error in name order, interrupted ones as not attempted) and `projectActionTimeout` (10 minutes); verify with unit tests using a fake action that fails some targets, a cancelled context and an expired timeout
- [x] 4.2 Implement `s` (stop running service containers, or start created/exited ones, otherwise report start/stop unavailable) and `r` (restart service containers that are not paused, otherwise report restart unavailable); one-off and paused containers are never targeted; verify with `dockerfake` tests for each spec scenario, using the task 1.5 hook for a refused stop of `myapp-db-1` reported with the engine's error text
- [x] 4.3 Implement the footer hints for the projects view (`enter containers`, `s stop`/`s start`/omitted, `r restart` omitted without a service container that is not paused, all omitted with no selection); verify with `keyhelp_test.go` cases

## 5. Compose column in containers views

- [x] 5.1 Add the column mode to containers views (`project` for the root and the image/volume drill-downs, `service` for the project drill-down, via an option on `NewFilteredContainers`) and the two extra column sets with the minimum widths from design.md; verify a unit test that each of the four column sets fits exactly 100 cells with the gutter and gaps
- [x] 5.2 Switch column sets per snapshot on `NumProjects() > 0`, keeping selection, filter and the stats layout, show `-` for unlabeled containers, and make the column a filter column; verify with tests for: no Compose containers (the containers table's columns and rows unchanged in the existing golden files), column shown while the only Compose container is hidden by `a`, column appearing on a new snapshot, SERVICE in a project drill-down, filtering by project, and new golden files for the ports and stats layouts at 100 columns
- [x] 5.3 Update the README (tab strip and counts, `1`-`4` and `:projects`/`pr` keys, the projects view keys table row, the PROJECT/SERVICE column, and that start only starts existing containers) and verify the keys table matches the help overlay

## 6. Integration checks

- [ ] 6.1 Add an integration test (build tag `integration`) that creates labeled containers in a test Compose-like project on a real engine, then checks the projects view row, stop and start of the project, and cleanup of the labeled resources; verify `go test -tags integration ./...` passes
- [ ] 6.2 Run the full local check (`go build ./...`, `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...` and with `--build-tags integration`, `go test -race ./...`) and verify all pass
- [ ] 6.3 Run `openspec validate add-compose-projects --strict` and verify it passes

## Workflow follow-up

- Manually try drydock against a real `docker compose up` stack before archiving.
- Archive the change with `/opsx:archive` after review.
