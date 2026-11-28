# Spec Delta

## Purpose

Defines how users view and operate on Docker networks in drydock: listing with usage information, inspection, deletion with protection for built-in networks, pruning unused networks, and navigating to the containers attached to a network.

## ADDED Requirements

### Requirement: Network list
The networks view SHALL list every network with the columns NAME, DRIVER, SCOPE, SUBNET and IN USE, sorted by name. IN USE is the number of containers, running or stopped, attached to the network. SUBNET shows the network's first IPv4 subnet, followed by `+N` when it has N more subnets of any family, the first subnet of any family when it has no IPv4 subnet, and `-` when it has none.

#### Scenario: Network in use
- **WHEN** network `myapp_default` has subnet `172.20.0.0/16` and containers `web` (running) and `db` (exited) are attached to it
- **THEN** its row shows SUBNET `172.20.0.0/16` and IN USE 2

#### Scenario: Several subnets
- **WHEN** network `dual` has subnets `172.21.0.0/16` and `fd00::/64`
- **THEN** its row shows SUBNET `172.21.0.0/16 +1`

#### Scenario: No subnet
- **WHEN** network `host` has no IPAM subnet
- **THEN** its row shows SUBNET `-`

#### Scenario: Sorted by name
- **WHEN** networks `zeta`, `bridge` and `alpha` exist
- **THEN** they are listed as `alpha`, `bridge`, `zeta`

### Requirement: Network list updates live
The networks view SHALL reflect network changes made outside drydock without user action, including networks created or removed and containers connected to or disconnected from a network.

#### Scenario: Network created elsewhere
- **WHEN** the networks view is open and `docker network create test-net` runs in another terminal
- **THEN** `test-net` appears in the list

#### Scenario: Container connected elsewhere
- **WHEN** network `test-net` shows IN USE 0 and `docker network connect test-net web` runs in another terminal, with no other engine activity
- **THEN** the `test-net` row shows IN USE 1

### Requirement: Built-in networks
The networks view SHALL treat `bridge`, `host` and `none`, and any network the engine marks as the Swarm ingress network, as built-in. Built-in networks SHALL be listed like any other network, SHALL NOT offer delete, and SHALL never be pruned.

#### Scenario: Delete not offered on a built-in network
- **WHEN** the selection is on `bridge`
- **THEN** the footer does not show `d`, and pressing `d` sends no request, shows no confirmation and shows an error that built-in networks cannot be removed

#### Scenario: Unused built-in network is not pruned
- **WHEN** no container is attached to `none` and the user presses `P`
- **THEN** `none` is neither listed in the confirmation nor removed

### Requirement: Delete network
The networks view SHALL delete the selected network after confirmation when the user presses `d`, without forcing removal or disconnecting containers. If containers are attached, the confirmation SHALL name them; with a running one it SHALL warn that the engine will refuse removal, and with only stopped ones that the network will be removed and they will not start until it is reconnected.

#### Scenario: Delete unused network
- **WHEN** the user presses `d` on unused network `old-net` and confirms
- **THEN** the network is removed and disappears from the list

#### Scenario: Delete network in use by a running container
- **WHEN** the user presses `d` on `myapp_default`, to which `web` (running) is attached
- **THEN** the confirmation lists `web` as attached and warns that the engine will refuse removal; if confirmed, the engine's refusal is shown as an error and the network remains

#### Scenario: Delete network used only by stopped containers
- **WHEN** the user presses `d` on `myapp_default`, to which only `db` (exited) is attached
- **THEN** the confirmation lists `db` and warns that it will not start until the network is reconnected; if confirmed, the network is removed

#### Scenario: Cancel the confirmation
- **WHEN** the delete confirmation is open and the user cancels it
- **THEN** no network is removed

### Requirement: Network inspect
The networks view SHALL open a scrollable inspect view showing the network's full engine inspection data as pretty-printed JSON when the user presses `i`.

#### Scenario: Inspect a network
- **WHEN** the user presses `i` on `myapp_default`
- **THEN** the inspect view shows its inspection JSON with breadcrumb `drydock > networks > myapp_default > inspect`

### Requirement: Prune unused networks
The networks view SHALL, when the user presses `P`, show a confirmation listing every network that is not built-in and has no container attached, running or stopped, and on confirm remove exactly those networks and report how many were removed. Networks whose only attached containers are stopped SHALL be kept.

#### Scenario: Prune unused networks
- **WHEN** networks `old-net` and `tmp-net` have no attached container and the user presses `P`
- **THEN** the confirmation lists `old-net` and `tmp-net`; on confirm both are removed and the status area reports 2 networks removed

#### Scenario: Network of stopped containers is kept
- **WHEN** every container of Compose project `myapp` is exited, they are attached to `myapp_default`, and the user prunes
- **THEN** `myapp_default` is neither listed in the confirmation nor removed

#### Scenario: Nothing to prune
- **WHEN** every network that is not built-in has an attached container and the user presses `P`
- **THEN** no confirmation is shown and the status area states there is nothing to prune

#### Scenario: Networks still loading
- **WHEN** the first snapshot has not loaded and the user presses `P`
- **THEN** no confirmation is shown and the status area states that networks are still loading

#### Scenario: Some removals fail
- **WHEN** the user confirms a prune of `old-net` and `tmp-net`, and the engine refuses to remove `tmp-net`
- **THEN** `old-net` is removed, and the status area reports 1 network removed and names `tmp-net` with the engine's error

### Requirement: Navigate to containers attached to a network
The networks view SHALL, when the user presses Enter on a network, push a containers view listing the containers, running and stopped, attached to that network. Like the other drill-downs, it SHALL start in all mode and follow the session's stats setting.

#### Scenario: Drill into a network
- **WHEN** the user presses Enter on `myapp_default`, to which `web` (running) and `db` (exited) are attached
- **THEN** a containers view listing `web` and `db` is shown with breadcrumb `drydock > networks > myapp_default > containers`, and Esc returns to the networks view

#### Scenario: Prune scope in a network drill-down
- **WHEN** the user drilled into `myapp_default`, exited container `db` is attached to it, exited container `other` is not, and the user presses `P`
- **THEN** the confirmation lists `db` and not `other`
