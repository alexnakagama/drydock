# Tasks

## 1. Engine access

- [x] 1.1 Add `NetworkList`, `NetworkInspect` (decoded data and raw JSON, like `VolumeInspect`) and `NetworkRemove` (by ID, never forced) to `docker.Service` and `SDKService`, with a `ValidateNetworkID` check before the SDK builds the request path (as `ValidateContainerID` does) and the fake's `MethodNetwork*` constants and stubs so `dockerfake` still satisfies `Service`; the SDK already returns errors that `docker.IsNotFound` recognizes, so nothing is mapped; verify `go build ./...`, a `ValidateNetworkID` table test in `docker_test.go`, and a `sdk_integration_test.go` test that creates, lists, inspects and removes a network and gets `IsNotFound` on a second removal
- [x] 1.2 Add `type=network` to the `Events` subscription filter and document it on `Service.Events`; verify with a test on the filter the SDK service builds
- [x] 1.3 Model networks in `dockerfake`: seeded built-in `bridge`/`host`/`none`, create/remove helpers, container attachments in `NetworkSettings.Networks` (with an option for an empty `NetworkID`), connect/disconnect helpers that emit only a `network` event, and a removal refusal for networks with attached running containers only (stopped containers do not block removal, as on the engine); verify with `fake_test.go` tests for each

## 2. Store

- [x] 2.1 Add networks to `Snapshot` (`Networks()`, `NumNetworks()`) and a `kindNetworks` reload in `store.go` (the `kind` constants and `String`, `reload`, and a `fire[kindNetworks]` case in `serve`); verify with `store_test.go` tests that the first snapshot and a `create`/`destroy` event deliver the network list
- [x] 2.2 Build `networkUsers` with a new `buildNetworkUsers(containers, networks)` (network ID -> containers, sorted like the other indexes, nil `NetworkSettings` and endpoints skipped, empty `NetworkID` resolved by name against the network list) whenever containers or networks reload, and add `Snapshot.NetworkUsers(id)`; verify with `snapshot_test.go` tests for running and stopped users, the empty-ID fallback, a recreated same-name network not inheriting users, and a container with `host` network mode, and a container with nil `NetworkSettings`
- [x] 2.3 Let `eventKind` return several kinds: network `connect`/`disconnect` refresh networks and containers, other network actions refresh networks; verify with a fake-backed store test where a connect with no other activity changes `NetworkUsers` after one debounce, and that a burst of connects reloads each kind once

## 3. Five resource tabs

- [x] 3.1 Extend `internal/ui/keys` to five tab keys (`Tab5`, `TabKeys()`, `tabKeys`/`switchKeys`, help `1-5/tab`), rename `TestFourTabKeys`, and update the `1-4/tab` assertions in `internal/ui/views/root_test.go`; verify `go test ./internal/ui/keys/ ./internal/ui/views/` passes (`TestSwitchKeysReserved` shows no view binds `5`)
- [x] 3.2 Update the root model tests (`model_test.go`) for five resources: number key `5`, `Tab` from projects to networks, wrap from networks to containers, `Shift+Tab` from containers to networks, the tab strip strings and narrow-alias width arithmetic; regenerate `root_100x30.golden` and add an 80-column case showing the alias fallback; verify `go test ./internal/ui/` passes

## 4. Networks view

- [ ] 4.1 Add the `keys.Networks` keymap (`enter` containers, `i` inspect, `d` delete, `P` prune unused; add it to `TestSwitchKeysReserved`) and `networksView` with the NAME, DRIVER, SCOPE, SUBNET and IN USE columns sorted by name, SUBNET formatting (first IPv4, `+N`, first of any family, invalid prefixes ignored, `-`), sanitized text, unused rows dimmed, `/` filter on NAME (as volumes), selection kept across snapshots and `ui.Positioner`; verify with unit tests for each SUBNET case, built directly on the view
- [ ] 4.2 Register the view as the fifth entry of `views.Resources()` (name `networks`, alias `net`, `Count: store.Snapshot.NumNetworks`) and in the test harness's own resource list (`harness_test.go`), update the harness's loaded-tabs strings, regenerate the view golden files with `-update` (reviewing that existing ones change only in the tab strip line) and add `networks_100x12.golden`; verify in `root_test.go` that `:networks`, `:net` and `5` open the view and the tab reads `5 networks (N)`, and that `go test ./internal/ui/views/` passes
- [ ] 4.3 Implement `i`: open the inspect view with the raw inspection JSON and breadcrumb `drydock > networks > <name> > inspect`; verify with a test and the existing inspect golden pattern
- [ ] 4.4 Implement the built-in rule (`bridge`, `host`, `none`, or `Ingress`) and `d`: on built-in networks the hint is omitted from `KeyHelp` and pressing `d` sends no request and shows an error; otherwise a confirmation naming attached containers, warning of the engine's refusal when one is running and that stopped ones will not start when none is, never forced; verify with tests for the delete, in-use refusal, stopped-only users, cancel and built-in scenarios
- [ ] 4.5 Implement `P` with `removal[network.Summary]`: candidates are non-built-in networks without `NetworkUsers`, confirmation lists them stating that networks used by running or stopped containers are kept, removal by ID with a local `errNetworkGone` wrapping not-found as gone; verify with tests for the prune, stopped-Compose-network-kept, nothing-to-prune, still-loading and partial-failure scenarios
- [ ] 4.6 Complete the footer hints (`enter containers`, `i inspect`, `P prune`; the `d delete` omission is done in 4.4) and help entries; verify with `keyhelp_test.go` cases

## 5. Network drill-down

- [ ] 5.1 Push `NewFilteredContainers` on Enter with crumbs `{name, "containers"}` and a predicate matching the network by ID, or by name for an empty `NetworkID`, the same rule as `buildNetworkUsers` (shared helper); it starts in all mode with shared `Prefs`; verify with tests for the breadcrumb, the scoped `P` confirmation, a created container with an empty `NetworkID` being listed, stats following the session setting, and Esc returning with the network still selected

## 6. Documentation

- [ ] 6.1 Update the README: tab strip and counts with five resources, `1`-`5` and `:networks`/`net`, the networks row of the keys table, unused networks dimmed, built-in networks not deletable, and how `P` differs from `docker network prune`; verify the keys table matches the help overlay

## 7. Integration checks

- [ ] 7.1 Add an integration test (build tag `integration`) that creates labeled networks and containers on a real engine and checks IN USE, the live update after `network connect`, that prune keeps a network whose only container is stopped and removes an unused one, and cleanup; verify `go test -tags integration ./...` passes
- [ ] 7.2 Run the full local check (`go build ./...`, `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...` and with `--build-tags integration`, `go test -race ./...`) and verify all pass
- [ ] 7.3 Run `openspec validate add-network-management --strict` and verify it passes
