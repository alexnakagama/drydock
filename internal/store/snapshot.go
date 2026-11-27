package store

import (
	"cmp"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
)

// Snapshot is an immutable view of the engine's containers (running and
// stopped), images, volumes and networks at one point in time, together
// with the usage indexes derived from them.
//
// A Snapshot is a value: copying it is cheap and the store never modifies
// one after publishing it. Accessors return fresh slices, so callers may
// sort or filter them freely, but the elements share nested slices and maps
// (for example container.Summary.Names) with the snapshot, and those must
// not be modified.
//
// Text fields (names, tags, labels, mountpoints, and every network field
// such as names, drivers, labels, options and IPAM data) come from the engine and
// are untrusted: they may contain terminal control sequences. The UI must
// pass them through docker.SanitizeText before rendering. ContainerName
// does so already.
//
// The zero Snapshot is empty and reports Loaded() == false.
type Snapshot struct {
	seq        uint64
	containers []container.Summary
	images     []image.Summary
	volumes    []volume.Volume
	networks   []network.Summary

	// Derived from containers; see buildIndexes.
	imageUsers  map[string][]container.Summary // image ID -> containers
	volumeUsers map[string][]container.Summary // volume name -> containers

	// Derived from containers and networks; see buildNetworkUsers.
	networkUsers map[string][]container.Summary // network ID -> containers

	// Derived from containers; see buildProjects.
	projects     map[string][]container.Summary // Compose project name -> containers
	projectNames []string                       // sorted
}

// Seq identifies the snapshot. It increases with every snapshot a store
// publishes, so a larger Seq is always newer.
func (s Snapshot) Seq() uint64 { return s.seq }

// Loaded reports whether the snapshot holds engine data, as opposed to
// being the zero value received before the first load.
func (s Snapshot) Loaded() bool { return s.seq > 0 }

// Containers returns every container, running or stopped, in engine order.
func (s Snapshot) Containers() []container.Summary { return slices.Clone(s.containers) }

// Images returns every top-level image, including dangling ones, in engine
// order. An image with several tags is a single element.
func (s Snapshot) Images() []image.Summary { return slices.Clone(s.images) }

// Volumes returns every volume, in engine order.
func (s Snapshot) Volumes() []volume.Volume { return slices.Clone(s.volumes) }

// Networks returns every network, including the engine's predefined ones,
// in engine order.
func (s Snapshot) Networks() []network.Summary { return slices.Clone(s.networks) }

// NumContainers returns len(Containers()) without copying.
func (s Snapshot) NumContainers() int { return len(s.containers) }

// NumImages returns len(Images()) without copying.
func (s Snapshot) NumImages() int { return len(s.images) }

// NumVolumes returns len(Volumes()) without copying.
func (s Snapshot) NumVolumes() int { return len(s.volumes) }

// NumNetworks returns len(Networks()) without copying.
func (s Snapshot) NumNetworks() int { return len(s.networks) }

// ImageUsers returns the containers, running or stopped, created from the
// image with the given ID (for example "sha256:..."), sorted by name. All
// tags of an image share its ID, so they share its users.
func (s Snapshot) ImageUsers(imageID string) []container.Summary {
	return slices.Clone(s.imageUsers[imageID])
}

// ImageUseCount returns len(ImageUsers(imageID)) without copying.
func (s Snapshot) ImageUseCount(imageID string) int { return len(s.imageUsers[imageID]) }

// VolumeUsers returns the containers, running or stopped, that mount the
// named volume, sorted by name. A container that mounts the volume at
// several paths is listed once.
func (s Snapshot) VolumeUsers(name string) []container.Summary {
	return slices.Clone(s.volumeUsers[name])
}

// VolumeUseCount returns len(VolumeUsers(name)) without copying.
func (s Snapshot) VolumeUseCount(name string) int { return len(s.volumeUsers[name]) }

// NetworkUsers returns the containers, running or stopped, attached to the
// network with the given ID, sorted by name. See AttachedToNetwork for
// which containers count as attached.
func (s Snapshot) NetworkUsers(networkID string) []container.Summary {
	return slices.Clone(s.networkUsers[networkID])
}

// NetworkUseCount returns len(NetworkUsers(networkID)) without copying.
func (s Snapshot) NetworkUseCount(networkID string) int { return len(s.networkUsers[networkID]) }

// ContainerName returns the container's primary name without the leading
// "/" the engine adds, or its short ID if it has no name. The result is safe
// to display: control characters are replaced (see docker.SanitizeText).
func ContainerName(c container.Summary) string {
	if len(c.Names) > 0 && c.Names[0] != "" {
		return docker.SanitizeText(strings.TrimPrefix(c.Names[0], "/"))
	}
	return docker.SanitizeText(ShortID(c.ID))
}

// ShortID returns the first 12 hex digits of an engine ID, without any
// "sha256:" prefix, as the docker CLI displays it.
func ShortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// buildIndexes computes the image and volume usage indexes from a container
// list. Each user list is sorted by container name (then ID) so that
// confirmation texts and drill-downs are stable across refreshes.
func buildIndexes(containers []container.Summary) (imageUsers, volumeUsers map[string][]container.Summary) {
	imageUsers = make(map[string][]container.Summary)
	volumeUsers = make(map[string][]container.Summary)
	for _, c := range containers {
		if c.ImageID != "" {
			imageUsers[c.ImageID] = append(imageUsers[c.ImageID], c)
		}
		var seen map[string]bool
		for _, m := range c.Mounts {
			if m.Type != mount.TypeVolume || m.Name == "" || seen[m.Name] {
				continue
			}
			if seen == nil {
				seen = make(map[string]bool, len(c.Mounts))
			}
			seen[m.Name] = true
			volumeUsers[m.Name] = append(volumeUsers[m.Name], c)
		}
	}
	for _, users := range imageUsers {
		sortContainers(users)
	}
	for _, users := range volumeUsers {
		sortContainers(users)
	}
	return imageUsers, volumeUsers
}

// buildNetworkUsers computes the network usage index (network ID ->
// containers) from a container list and the network list, applying the
// rule of AttachedToNetwork: an endpoint belongs to the network whose ID it
// carries, or, when its NetworkID is empty (a container created but never
// started), to the listed networks named by its key. Nil NetworkSettings and
// nil endpoints are skipped, and a container is listed once per network.
// Each user list is sorted like the other indexes.
func buildNetworkUsers(containers []container.Summary, networks []network.Summary) map[string][]container.Summary {
	users := make(map[string][]container.Summary)
	if len(containers) == 0 {
		return users
	}
	// Names are unique on the engine, but should two networks share one, an
	// endpoint known only by that name counts toward each of them, as
	// AttachedToNetwork does, so that prune never treats either as unused.
	byName := make(map[string][]string, len(networks))
	for _, n := range networks {
		if n.Name != "" && n.ID != "" {
			byName[n.Name] = append(byName[n.Name], n.ID)
		}
	}
	for _, c := range containers {
		if c.NetworkSettings == nil {
			continue
		}
		var seen map[string]bool
		add := func(id string) {
			if id == "" || seen[id] {
				return
			}
			if seen == nil {
				seen = make(map[string]bool, len(c.NetworkSettings.Networks))
			}
			seen[id] = true
			users[id] = append(users[id], c)
		}
		for key, ep := range c.NetworkSettings.Networks {
			if id, byID := endpointNetworkID(ep); byID {
				add(id)
			} else if ep != nil && key != "" {
				for _, id := range byName[key] {
					add(id)
				}
			}
		}
	}
	for _, us := range users {
		sortContainers(us)
	}
	return users
}

// AttachedToNetwork reports whether container c is attached to the network
// with the given ID and name: one of its endpoints carries that network ID,
// or has no NetworkID (the engine lists a container that was created but
// never started that way) and is stored under that name. Keying by ID keeps
// a container attached to a removed network from counting toward a new
// network created with the same name. It is the rule Snapshot.NetworkUsers
// applies, for callers that cannot read a snapshot.
func AttachedToNetwork(c container.Summary, networkID, name string) bool {
	if networkID == "" || c.NetworkSettings == nil {
		return false
	}
	for key, ep := range c.NetworkSettings.Networks {
		if id, byID := endpointNetworkID(ep); byID {
			if id == networkID {
				return true
			}
		} else if ep != nil && key != "" && key == name {
			return true
		}
	}
	return false
}

// endpointNetworkID returns the network ID an endpoint carries and true,
// or false when the endpoint is nil or has no NetworkID (a container that
// was created but never started), in which case only its map key, the
// network name, identifies the network.
func endpointNetworkID(ep *network.EndpointSettings) (string, bool) {
	if ep == nil || ep.NetworkID == "" {
		return "", false
	}
	return ep.NetworkID, true
}

func sortContainers(cs []container.Summary) {
	slices.SortFunc(cs, func(a, b container.Summary) int {
		return cmp.Or(cmp.Compare(ContainerName(a), ContainerName(b)), cmp.Compare(a.ID, b.ID))
	})
}
