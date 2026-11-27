# Design

## Context

Volumes already have the shape this change needs: the store lists them,
`buildIndexes` derives `volumeUsers` from the container list, the volumes
view previews its own prune set and removes it with `removal[T]`, and
`Enter` pushes a containers view scoped to the users. Networks follow the
same path. Two parts of the existing plumbing do not carry over:

- `SDKService.Events` subscribes only to `container`, `image` and `volume`
  events, and `eventKind` maps nothing to networks.
- Volume usage stays current through container events, because mounting is
  part of a container's lifecycle. Network attachment is not:
  `docker network connect`/`disconnect` emit only a `network` event, yet
  they change `container.Summary.NetworkSettings.Networks`.

The engine's `NetworkPrune` removes networks without active endpoints, and
stopped containers hold none. See proposal.md for why drydock keeps those
networks.

## Goals / Non-Goals

**Goals:**
- Networks reuse the store, snapshot, removal and drill-down machinery
  without new abstractions.
- IN USE and the prune set always agree: both come from the same index.

**Non-Goals:**
- Reading a network's own `Containers` map (only `NetworkInspect` fills it,
  and only with running endpoints).
- Any write to a network other than removal.

## Decisions

### Network users come from the container list, keyed by network ID

A new `buildNetworkUsers(containers, networks)` next to `buildIndexes` builds
`networkUsers` (network ID -> containers, sorted like the others) from each
container's `NetworkSettings.Networks`. It is a separate function because it
needs the network list too, and `buildIndexes(cs)` and its callers in
`snapshot_test.go` stay as they are. `NetworkSettings` is a pointer and the
map's values are pointers, so nil ones are skipped. Each entry carries the
network's ID in `NetworkID`. When it is empty (the engine reports it for
containers that were created but never started), the entry's map key, the
network name, is resolved against the snapshot's network list instead, so
`reload` rebuilds the index whenever either list reloads.

Keying by ID rather than name keeps a container attached to a network that
was removed and recreated with the same name from counting toward the new
one; the engine would not reconnect it either.

*Alternative:* call `NetworkInspect` per network and read `Containers`.
Rejected: one request per network on every refresh, and it misses stopped
containers, which is exactly what prune must see.

### Network events reload networks; connect and disconnect also reload containers

The subscription adds `type=network`. `eventKind` becomes able to return
more than one kind: `create`/`destroy` (and any other network action) map to
networks; `connect`/`disconnect` map to networks and containers. Each kind
keeps its own debounce, so a `docker compose up` burst still coalesces into
one reload per kind.

*Alternative:* treat connect/disconnect as networks only and rebuild the
index from the old container list. Rejected: the container list itself is
what changed, so the index would be stale until some unrelated container
event.

### Built-in is a name check plus the Ingress flag

The API has no "predefined" field. A network is built-in when its name is
`bridge`, `host` or `none` (the engine's fixed predefined set, which it
refuses to remove) or `Ingress` is true. `docker_gwbridge` is an ordinary
bridge network to the engine and is not special-cased: while Swarm uses it
the engine refuses removal, and that refusal is reported like any other.

### Prune previews and removes by ID with `removal[T]`

`P` computes the candidates from the current snapshot: not built-in and no
entry in `networkUsers`. The confirmation lists them by name; on confirm
`removal[network.Summary]` removes each by ID, carrying on past failures.
`remove` wraps a not-found error with a local `errNetworkGone` (like
`errContainerGone`), which is the `gone` sentinel. IDs are never reused, so
unlike volumes no identity check is needed. A container attached
between preview and confirm makes the engine refuse that one network, which
is reported by name.

*Alternative:* `NetworkPrune`. Rejected, see Context.

### Delete hides `d` for built-in networks and never forces

The footer already hides inapplicable keys; built-in networks join that
rule. Pressing the hidden key sends no request and shows an error that
built-in networks cannot be removed, like the hidden `x` on a stopped
container. For other networks the confirmation lists `networkUsers` names,
the same way volume delete does. The engine refuses to remove a network only
while a running container has an endpoint on it; stopped containers hold
none, so a network used only by stopped containers is removed. The warning
therefore depends on the users: with a running one it says the engine will
refuse removal; with only stopped ones it says the network will be removed
and they will not start until it is reconnected.

### Drill-down is a new containers scope

The view is `NewFilteredContainers` with a predicate, like the volume
drill-down: it matches a container attached to the network by ID, or by name
when the entry's `NetworkID` is empty, which is the rule `buildNetworkUsers`
applies (a helper shared by both keeps them from drifting). The predicate
cannot read the snapshot, so it captures the network's ID and name. Its
existing scope-aware prune (`pruneCandidates` uses the predicate), all-mode
start and shared `Prefs` then apply unchanged. The breadcrumb uses the
network name.

### SUBNET formatting

Taken from `IPAM.Config` of the list summary, where `Subnet` is a
`netip.Prefix` that is the zero value when an entry has none; such entries
are ignored. The first IPv4 subnet is shown (or the first subnet when none
is IPv4), then `+N` for the remaining subnets. All text goes through `docker.SanitizeText`.

### Fifth resource

`Resources()` appends `networks` (alias `net`, count `NumNetworks`). The tab
strip, number keys, `Tab` cycling and command bar are already driven by
that list; the changes there are spec and test updates, plus checking the
narrow alias layout with five tabs.

## Risks / Trade-offs

- [Prune differs from `docker network prune`] -> Stated in the README and in
  the confirmation's wording ("not used by any container, running or
  stopped").
- [A container whose `NetworkID` is empty and whose network name no longer
  exists is counted toward nothing] -> Correct: that network is gone, so
  nothing can be pruned or shown for it.
- [Connect/disconnect now reload the container list, which is the largest
  list] -> Debounced like container events; these events are rare outside
  `compose up`, which already triggers container reloads.
- [Five full-width tabs no longer fit 80 columns] -> The existing alias
  fallback (`5 net (6)`) applies; covered by a golden test at 80 columns.
- [Fake engine drift] -> The fake models connect/disconnect as network-only
  events, matching the real engine, so the live-update scenario is tested
  without the real engine; an integration test confirms it against one.
