package store

import (
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
)

func ctr(id, name, imageID string, state container.ContainerState, mounts ...container.MountPoint) container.Summary {
	return container.Summary{
		ID: id, Names: []string{"/" + name}, ImageID: imageID, State: state, Mounts: mounts,
	}
}

func volMount(name, dest string) container.MountPoint {
	return container.MountPoint{Type: mount.TypeVolume, Name: name, Destination: dest}
}

func containerNames(cs []container.Summary) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, ContainerName(c))
	}
	return out
}

func TestBuildIndexes(t *testing.T) {
	t.Parallel()
	const nginx, app = "sha256:aaa", "sha256:bbb"
	cs := []container.Summary{
		ctr("1", "web", nginx, container.StateRunning, volMount("pgdata", "/a")),
		ctr("2", "old", nginx, container.StateExited),
		// Mounts the same volume twice: counted once.
		ctr("3", "db", app, container.StateExited, volMount("pgdata", "/b"), volMount("pgdata", "/c"),
			container.MountPoint{Type: mount.TypeBind, Source: "/host", Destination: "/d"}),
		ctr("4", "noimage", "", container.StateCreated, volMount("cache", "/e")),
	}
	snap := Snapshot{seq: 1, containers: cs}
	snap.imageUsers, snap.volumeUsers = buildIndexes(cs)

	if got := snap.ImageUseCount(nginx); got != 2 {
		t.Errorf("ImageUseCount(nginx) = %d, want 2 (running and stopped)", got)
	}
	if got := containerNames(snap.ImageUsers(nginx)); !slices.Equal(got, []string{"old", "web"}) {
		t.Errorf("ImageUsers(nginx) = %v, want sorted [old web]", got)
	}
	if got := snap.ImageUseCount(app); got != 1 {
		t.Errorf("ImageUseCount(app) = %d, want 1", got)
	}
	if got := snap.ImageUseCount(""); got != 0 {
		t.Errorf("containers without an image ID are indexed: %d", got)
	}
	if got := containerNames(snap.VolumeUsers("pgdata")); !slices.Equal(got, []string{"db", "web"}) {
		t.Errorf("VolumeUsers(pgdata) = %v, want [db web]", got)
	}
	if got := snap.VolumeUseCount("cache"); got != 1 {
		t.Errorf("VolumeUseCount(cache) = %d, want 1", got)
	}
	if got := snap.VolumeUseCount("/host"); got != 0 {
		t.Errorf("bind mounts are indexed as volumes: %d", got)
	}
	if got := snap.VolumeUseCount("missing"); got != 0 {
		t.Errorf("VolumeUseCount(missing) = %d, want 0", got)
	}
}

// All tags of an image share its ID, so every tag row reports the same
// IN USE count.
func TestMultiTagImageUsage(t *testing.T) {
	t.Parallel()
	const id = "sha256:ccc"
	cs := []container.Summary{
		ctr("1", "a", id, container.StateRunning),
		ctr("2", "b", id, container.StateExited),
	}
	snap := Snapshot{seq: 1, containers: cs}
	snap.imageUsers, snap.volumeUsers = buildIndexes(cs)
	// Rows "app:latest" and "app:1.0" both look up the image ID.
	for _, tag := range []string{"app:latest", "app:1.0"} {
		if got := snap.ImageUseCount(id); got != 2 {
			t.Errorf("%s: IN USE = %d, want 2", tag, got)
		}
	}
}

func TestSnapshotAccessorsReturnCopies(t *testing.T) {
	t.Parallel()
	cs := []container.Summary{ctr("1", "b", "sha256:x", container.StateRunning), ctr("2", "a", "sha256:x", container.StateRunning)}
	snap := Snapshot{seq: 1, containers: cs}
	snap.imageUsers, snap.volumeUsers = buildIndexes(cs)

	got := snap.Containers()
	got[0].ID = "mutated"
	slices.Reverse(got)
	if snap.Containers()[0].ID != "1" {
		t.Error("modifying the result of Containers changed the snapshot")
	}
	users := snap.ImageUsers("sha256:x")
	users[0] = container.Summary{}
	if ContainerName(snap.ImageUsers("sha256:x")[0]) != "a" {
		t.Error("modifying the result of ImageUsers changed the snapshot")
	}
}

func TestZeroSnapshot(t *testing.T) {
	t.Parallel()
	var snap Snapshot
	if snap.Loaded() {
		t.Error("zero snapshot reports Loaded")
	}
	if len(snap.Containers()) != 0 || len(snap.Images()) != 0 || len(snap.Volumes()) != 0 ||
		snap.ImageUseCount("x") != 0 || snap.VolumeUseCount("x") != 0 {
		t.Error("zero snapshot is not empty")
	}
}

func TestContainerNameAndShortID(t *testing.T) {
	t.Parallel()
	if got := ContainerName(container.Summary{Names: []string{"/web"}}); got != "web" {
		t.Errorf("ContainerName = %q, want web", got)
	}
	if got := ContainerName(container.Summary{ID: "0123456789abcdef0123"}); got != "0123456789ab" {
		t.Errorf("ContainerName without names = %q, want short ID", got)
	}
	if got := ShortID("sha256:0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("ShortID = %q", got)
	}
	if got := ContainerName(container.Summary{Names: []string{"/evil\x1b[2J\x1b]0;title\a"}}); strings.ContainsAny(got, "\x1b\a") {
		t.Errorf("ContainerName keeps control characters: %q", got)
	}
	if got := ShortID("abc"); got != "abc" {
		t.Errorf("ShortID(short) = %q", got)
	}
}
