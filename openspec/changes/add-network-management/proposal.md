# Proposal

## Why

Networks are the only core Docker resource drydock does not show. To see
which networks exist, which containers are attached to them, or to clean up
the networks left behind by old Compose projects and test runs, users still
have to leave drydock for `docker network ls`, `inspect` and `prune`. The
engine API and the container list already carry everything needed.

## What Changes

- Add a fifth resource, **networks**: a tab (`5`, `:networks`, alias `net`)
  listing every network with the columns NAME, DRIVER, SCOPE, SUBNET and
  IN USE (containers attached, running or stopped), sorted by name. Networks
  no container uses are dimmed, like unused volumes.
- `Enter` on a network opens a drill-down containers view of the containers
  attached to it.
- `i` opens the network's inspection JSON.
- `d` deletes the selected network after confirmation. When containers are
  attached, the confirmation names them: with a running one it warns that
  the engine will refuse removal; with only stopped ones it warns that the
  network will be removed and they will not start until it is reconnected.
  Removal is never forced. `d` is not offered on built-in networks
  (`bridge`, `host`, `none`, and the Swarm ingress network).
- `P` prunes unused networks: drydock lists every network that is not
  built-in and that no container uses, **running or stopped**, and on
  confirm removes exactly those. Unlike `docker network prune`, a network
  whose only users are stopped containers (for example a stopped Compose
  project's `<project>_default`) is kept, so starting those containers again
  still works. The README states this difference.
- The tab strip, number keys, `Tab` cycling, command bar and help cover the
  fifth resource; the networks count is the engine total.
- Network changes made outside drydock appear live, including
  `docker network connect`/`disconnect`, which change a container's
  attached networks without a container event.

Out of scope: creating networks; connecting or disconnecting containers;
a PROJECT column on networks; forced removal; Swarm services and overlay
network management beyond listing.

## Capabilities

### New Capabilities
- `network-management`: the networks view: its rows, columns and usage
  count, inspect, delete with built-in protection, the drydock-computed
  prune of unused networks, and the drill-down into attached containers.

### Modified Capabilities
- `tui-navigation`: the command bar accepts `networks`/`net`; the tab strip
  lists five resources with a networks count; number keys go up to `5` and
  `Tab` cycles through networks; unused networks are dimmed.

## Impact

- `internal/docker`: `Service` gains `NetworkList`, `NetworkInspect` and
  `NetworkRemove`; the event subscription adds the `network` type; the fake
  engine models networks and container attachments.
- `internal/store`: the snapshot holds networks, a network-users index
  derived from each container's attached networks, and `NumNetworks`; network
  events refresh networks, and `connect`/`disconnect` also refresh containers.
- `internal/ui/views`: a new networks view and its registration as the fifth
  resource; the containers view gains a network drill-down scope.
- README: tab list, key tables, prune behavior.
- No new dependencies; the Docker Engine SDK already provides the calls.
