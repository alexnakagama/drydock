package dockerfake

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
)

const waitTimeout = 2 * time.Second

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func running(name, imageRef, imageID string, mounts ...container.MountPoint) container.Summary {
	return container.Summary{
		Names: []string{"/" + name}, Image: imageRef, ImageID: imageID,
		State: container.StateRunning, Status: "Up 1 minute", Mounts: mounts,
	}
}

func exited(name, imageRef, imageID string, mounts ...container.MountPoint) container.Summary {
	c := running(name, imageRef, imageID, mounts...)
	c.State, c.Status = container.StateExited, "Exited (0) 1 minute ago"
	return c
}

func volMount(name string) container.MountPoint {
	return container.MountPoint{Type: mount.TypeVolume, Name: name, Destination: "/data"}
}

func names(cs []container.Summary) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Names[0])
	}
	return out
}

// nextEvent waits for the next event on es.
func nextEvent(t *testing.T, es docker.EventStream) events.Message {
	t.Helper()
	select {
	case m := <-es.Messages:
		return m
	case err := <-es.Err:
		t.Fatalf("event stream ended: %v", err)
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for an event")
	}
	return events.Message{}
}

func streamErr(t *testing.T, es docker.EventStream) error {
	t.Helper()
	select {
	case err := <-es.Err:
		return err
	case m := <-es.Messages:
		t.Fatalf("unexpected event %s/%s", m.Type, m.Action)
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for the event stream to end")
	}
	return nil
}

func TestContainerListRunningAndAll(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	f.AddContainer(running("web", "nginx", "sha256:a"))
	f.AddContainer(exited("old", "nginx", "sha256:a"))

	got, err := f.ContainerList(ctx, docker.ContainerListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/web"}; !slices.Equal(names(got), want) {
		t.Errorf("running list = %v, want %v", names(got), want)
	}
	got, err = f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/web", "/old"}; !slices.Equal(names(got), want) {
		t.Errorf("all list = %v, want %v", names(got), want)
	}

	// Returned values are copies.
	got[0].Names[0] = "/mutated"
	again, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	if again[0].Names[0] != "/web" {
		t.Error("ContainerList returned shared state")
	}
}

func TestContainerLifecycle(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(exited("old", "nginx", "sha256:a"))

	if err := f.ContainerStart(ctx, id); err != nil {
		t.Fatal(err)
	}
	insp, err := f.ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !insp.Container.State.Running || len(insp.Raw) == 0 {
		t.Errorf("after start: running=%v raw=%q", insp.Container.State.Running, insp.Raw)
	}
	if err := f.ContainerStop(ctx, "old"); err != nil { // by name
		t.Fatal(err)
	}
	if err := f.ContainerRestart(ctx, id[:12]); err != nil { // by ID prefix
		t.Fatal(err)
	}
	cs, _ := f.ContainerList(ctx, docker.ContainerListOptions{})
	if len(cs) != 1 {
		t.Fatalf("after restart: %d running containers, want 1", len(cs))
	}

	paused := running("p", "nginx", "sha256:a")
	paused.State = container.StatePaused
	pid := f.AddContainer(paused)
	if err := f.ContainerStart(ctx, pid); !docker.IsConflict(err) {
		t.Errorf("start paused: err = %v, want conflict", err)
	}
	if err := f.ContainerStart(ctx, "missing"); !docker.IsNotFound(err) {
		t.Errorf("start missing: err = %v, want not found", err)
	}
	if err := f.ContainerStart(ctx, "../images/x"); !docker.IsInvalidArgument(err) {
		t.Errorf("start with path traversal: err = %v, want invalid argument", err)
	}
}

func TestContainerPauseUnpause(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:a"))
	es := f.Events(ctx)

	state := func(when string) (container.ContainerState, string) {
		t.Helper()
		cs, err := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
		if err != nil || len(cs) != 1 {
			t.Fatalf("%s: list = %v, %v", when, cs, err)
		}
		return cs[0].State, cs[0].Status
	}
	expectEvent := func(action events.Action) {
		t.Helper()
		m := nextEvent(t, es)
		if m.Type != events.ContainerEventType || m.Action != action || m.Actor.ID != id || m.Actor.Attributes["name"] != "web" {
			t.Errorf("event = %s/%s %s %v, want container/%s %s web", m.Type, m.Action, m.Actor.ID, m.Actor.Attributes, action, id)
		}
	}

	if err := f.ContainerPause(ctx, "web"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	expectEvent(events.ActionPause)
	if st, status := state("after pause"); st != container.StatePaused || status != "Up 1 minute (Paused)" {
		t.Errorf("after pause: state %q status %q", st, status)
	}
	if insp, err := f.ContainerInspect(ctx, id); err != nil || !insp.Container.State.Paused || !insp.Container.State.Running {
		t.Errorf("after pause: inspect = %+v (%v), want Running and Paused", insp.Container.State, err)
	}
	if err := f.ContainerPause(ctx, id); !docker.IsConflict(err) {
		t.Errorf("pause a paused container: err = %v, want conflict", err)
	}

	if err := f.ContainerUnpause(ctx, id[:12]); err != nil {
		t.Fatalf("unpause: %v", err)
	}
	expectEvent(events.ActionUnPause)
	if st, status := state("after unpause"); st != container.StateRunning || status != "Up 1 minute" {
		t.Errorf("after unpause: state %q status %q", st, status)
	}
	if err := f.ContainerUnpause(ctx, id); !cerrdefs.IsInternal(err) {
		t.Errorf("unpause a running container: err = %v, want internal (the engine's 500)", err)
	}

	calls := f.CallsTo(MethodContainerPause)
	if len(calls) != 2 || calls[0].Args[0] != "web" {
		t.Errorf("ContainerPause calls = %+v", calls)
	}
	if n := len(f.CallsTo(MethodContainerUnpause)); n != 2 {
		t.Errorf("recorded %d ContainerUnpause calls, want 2", n)
	}
}

func TestContainerPauseUnpauseErrors(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	old := f.AddContainer(exited("old", "nginx", "sha256:a"))
	restarting := running("flaky", "nginx", "sha256:a")
	restarting.State = container.StateRestarting
	flaky := f.AddContainer(restarting)
	es := f.Events(ctx)

	for _, tc := range []struct {
		name string
		call func() error
		want func(error) bool
	}{
		{"pause exited", func() error { return f.ContainerPause(ctx, old) }, docker.IsConflict},
		{"unpause exited", func() error { return f.ContainerUnpause(ctx, old) }, cerrdefs.IsInternal},
		{"pause restarting", func() error { return f.ContainerPause(ctx, flaky) }, docker.IsConflict},
	} {
		if err := tc.call(); !tc.want(err) {
			t.Errorf("%s: err = %v (%T)", tc.name, err, err)
		}
	}
	cs, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	for _, c := range cs {
		if c.State != container.StateExited && c.State != container.StateRestarting {
			t.Errorf("%s: state changed to %q by a refused call", c.Names[0], c.State)
		}
	}

	for _, call := range []func(context.Context, string) error{f.ContainerPause, f.ContainerUnpause} {
		if err := call(ctx, "missing"); !docker.IsNotFound(err) {
			t.Errorf("missing container: err = %v, want not found", err)
		}
		if err := call(ctx, "../images/x"); !docker.IsInvalidArgument(err) {
			t.Errorf("path traversal: err = %v, want invalid argument", err)
		}
	}

	// A refused call emits nothing: the next event is the one added below.
	f.AddVolume(volume.Volume{Name: "marker"})
	if m := nextEvent(t, es); m.Type != events.VolumeEventType || m.Actor.ID != "marker" {
		t.Errorf("refused calls emitted %s/%s %s", m.Type, m.Action, m.Actor.ID)
	}
}

func TestContainerPauseUnpauseDisconnected(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	web := running("web", "nginx", "sha256:a")
	id := f.AddContainer(web)
	pausedC := running("db", "pg", "sha256:b")
	pausedC.State = container.StatePaused
	db := f.AddContainer(pausedC)

	f.Disconnect(nil)
	if err := f.ContainerPause(ctx, id); err == nil || docker.ClassifyConnectError(err) != docker.FailureNotRunning {
		t.Errorf("pause while disconnected: err = %v, want a connection error", err)
	}
	if err := f.ContainerUnpause(ctx, db); err == nil || docker.ClassifyConnectError(err) != docker.FailureNotRunning {
		t.Errorf("unpause while disconnected: err = %v, want a connection error", err)
	}

	f.Reconnect()
	cs, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	for _, c := range cs {
		if want := map[string]container.ContainerState{id: container.StateRunning, db: container.StatePaused}[c.ID]; c.State != want {
			t.Errorf("%s: state %q after disconnected calls, want %q", c.Names[0], c.State, want)
		}
	}
	if err := f.ContainerPause(ctx, id); err != nil {
		t.Errorf("pause after reconnect: %v", err)
	}
}

func TestContainerStopPausedClearsPausedStatus(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:a"))
	if err := f.ContainerPause(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := f.ContainerStop(ctx, id); err != nil {
		t.Fatal(err)
	}
	cs, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	if len(cs) != 1 || cs[0].State != container.StateExited || strings.Contains(cs[0].Status, "Paused") {
		t.Errorf("after stopping a paused container: %+v", cs)
	}
	if insp, err := f.ContainerInspect(ctx, id); err != nil || insp.Container.State.Running || insp.Container.State.Paused {
		t.Errorf("after stop: inspect = %+v (%v), want neither Running nor Paused", insp.Container.State, err)
	}
}

func TestContainerPauseKeepsStatsStream(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:a"))
	st, err := f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := f.ContainerPause(ctx, id); err != nil {
		t.Fatal(err)
	}
	if n := f.StatsStreams(); n != 1 {
		t.Errorf("open stats streams after pause = %d, want 1", n)
	}
}

func TestContainerRemove(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	f.AddVolume(volume.Volume{Name: "named"})
	f.AddVolume(volume.Volume{Name: "anon", Labels: map[string]string{AnonymousVolumeLabel: ""}})
	web := f.AddContainer(running("web", "nginx", "sha256:a", volMount("named"), volMount("anon")))

	err := f.ContainerRemove(ctx, web, docker.ContainerRemoveOptions{})
	if !docker.IsConflict(err) {
		t.Fatalf("remove running without force: err = %v, want conflict", err)
	}
	if err := f.ContainerRemove(ctx, web, docker.ContainerRemoveOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	vs, _ := f.VolumeList(ctx)
	if len(vs) != 2 {
		t.Errorf("RemoveVolumes=false removed volumes: %d left, want 2", len(vs))
	}

	calls := f.CallsTo(MethodContainerRemove)
	if len(calls) != 2 {
		t.Fatalf("recorded %d ContainerRemove calls, want 2", len(calls))
	}
	if got := calls[1].Args[1].(docker.ContainerRemoveOptions); got != (docker.ContainerRemoveOptions{Force: true}) {
		t.Errorf("recorded options = %+v", got)
	}

	db := f.AddContainer(exited("db", "pg", "sha256:b", volMount("named"), volMount("anon")))
	if err := f.ContainerRemove(ctx, db, docker.ContainerRemoveOptions{RemoveVolumes: true}); err != nil {
		t.Fatal(err)
	}
	vs, _ = f.VolumeList(ctx)
	if len(vs) != 1 || vs[0].Name != "named" {
		t.Errorf("RemoveVolumes=true: volumes = %+v, want only the named volume", vs)
	}
}

func readAllLogs(t *testing.T, rc io.ReadCloser, tty bool) (stdout, stderr string) {
	t.Helper()
	defer func() { _ = rc.Close() }()
	if tty {
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read logs: %v", err)
		}
		return string(b), ""
	}
	var o, e bytes.Buffer
	if _, err := stdcopy.StdCopy(&o, &e, rc); err != nil {
		t.Fatalf("demultiplex logs: %v", err)
	}
	return o.String(), e.String()
}

func TestContainerLogs(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:a"))
	f.WriteLog(id, false, "first\n")
	f.WriteLog(id, true, "oops\n")
	f.WriteLog(id, false, "123456789\n") // 10-byte payload: a 0x0A length byte in the frame header
	f.WriteLog(id, false, "last\n")

	tests := []struct {
		name             string
		opts             docker.LogsOptions
		wantOut, wantErr string
	}{
		{"all by default", docker.LogsOptions{}, "first\n123456789\nlast\n", "oops\n"},
		{"negative tail is all", docker.LogsOptions{Tail: -1}, "first\n123456789\nlast\n", "oops\n"},
		{"tail counts entries", docker.LogsOptions{Tail: 2}, "123456789\nlast\n", ""},
		{"tail larger than history", docker.LogsOptions{Tail: 100}, "first\n123456789\nlast\n", "oops\n"},
		{"stdout only", docker.LogsOptions{Stdout: true}, "first\n123456789\nlast\n", ""},
		{"stderr only", docker.LogsOptions{Stderr: true, Tail: 1}, "", "oops\n"},
	}
	for _, tt := range tests {
		rc, err := f.ContainerLogs(ctx, id, tt.opts)
		if err != nil {
			t.Fatal(err)
		}
		gotOut, gotErr := readAllLogs(t, rc, false)
		if gotOut != tt.wantOut || gotErr != tt.wantErr {
			t.Errorf("%s: stdout %q stderr %q; want %q, %q", tt.name, gotOut, gotErr, tt.wantOut, tt.wantErr)
		}
	}

	// A TTY container's log is the raw payload, interleaved.
	f.SetTTY(id, true)
	rc, err := f.ContainerLogs(ctx, id, docker.LogsOptions{Tail: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := readAllLogs(t, rc, true); got != "oops\n123456789\nlast\n" {
		t.Errorf("tty log = %q", got)
	}
	f.SetTTY(id, false)

	// A follower receives new output and ends when the container stops.
	rc, err = f.ContainerLogs(ctx, id, docker.LogsOptions{Tail: 1, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	type result struct{ out, errOut string }
	done := make(chan result, 1)
	go func() {
		var o, e bytes.Buffer
		_, _ = stdcopy.StdCopy(&o, &e, rc)
		done <- result{o.String(), e.String()}
	}()
	f.WriteLog(id, false, "new\n")
	if err := f.ContainerStop(ctx, id); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.out != "last\nnew\n" {
			t.Errorf("follow stdout = %q, want %q", r.out, "last\nnew\n")
		}
	case <-time.After(waitTimeout):
		t.Fatal("follower did not end after stop")
	}

	// Cancelling the context, or closing the reader, ends a follower.
	if err := f.ContainerStart(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, viaClose := range []bool{false, true} {
		fctx, cancel := context.WithCancel(ctx)
		rc, _ = f.ContainerLogs(fctx, id, docker.LogsOptions{Tail: 1, Follow: true})
		errc := make(chan error, 1)
		go func() {
			_, err := io.ReadAll(rc)
			errc <- err
		}()
		if viaClose {
			if err := rc.Close(); err != nil {
				t.Errorf("close follower: %v", err)
			}
		} else {
			cancel()
		}
		select {
		case err := <-errc:
			if err == nil {
				t.Errorf("follower ended without error (close=%v)", viaClose)
			}
		case <-time.After(waitTimeout):
			t.Fatalf("follower did not end (close=%v)", viaClose)
		}
		cancel()
	}
}

func TestImageRemove(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)

	t.Run("unused", func(t *testing.T) {
		f := New()
		f.AddImage(image.Summary{ID: "sha256:aaa", RepoTags: []string{"alpine:3.20"}})
		resp, err := f.ImageRemove(ctx, "alpine:3.20", docker.ImageRemoveOptions{PruneChildren: true})
		if err != nil {
			t.Fatal(err)
		}
		if want := []image.DeleteResponse{{Untagged: "alpine:3.20"}, {Deleted: "sha256:aaa"}}; !slices.Equal(resp, want) {
			t.Errorf("resp = %+v, want %+v", resp, want)
		}
		if imgs, _ := f.ImageList(ctx); len(imgs) != 0 {
			t.Errorf("images left: %+v", imgs)
		}
	})

	t.Run("shared tag only untags, even in use", func(t *testing.T) {
		f := New()
		f.AddImage(image.Summary{ID: "sha256:bbb", RepoTags: []string{"nginx:1.27", "nginx:latest"}})
		f.AddContainer(running("web", "nginx:1.27", "sha256:bbb"))
		resp, err := f.ImageRemove(ctx, "nginx:1.27", docker.ImageRemoveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if want := []image.DeleteResponse{{Untagged: "nginx:1.27"}}; !slices.Equal(resp, want) {
			t.Errorf("resp = %+v", resp)
		}
		imgs, _ := f.ImageList(ctx)
		if len(imgs) != 1 || !slices.Equal(imgs[0].RepoTags, []string{"nginx:latest"}) {
			t.Errorf("images = %+v", imgs)
		}
	})

	t.Run("last tag in use is refused", func(t *testing.T) {
		f := New()
		f.AddImage(image.Summary{ID: "sha256:ccc", RepoTags: []string{"nginx:1.27"}})
		f.AddContainer(exited("old", "nginx:1.27", "sha256:ccc"))
		if _, err := f.ImageRemove(ctx, "nginx:1.27", docker.ImageRemoveOptions{}); !docker.IsConflict(err) {
			t.Errorf("err = %v, want conflict", err)
		}
		if imgs, _ := f.ImageList(ctx); len(imgs) != 1 || imgs[0].Containers != 1 {
			t.Errorf("images = %+v, want the image kept with 1 container", imgs)
		}
	})

	t.Run("by ID with several tags needs force", func(t *testing.T) {
		f := New()
		f.AddImage(image.Summary{ID: "sha256:ddd", RepoTags: []string{"app:1", "app:2"}})
		if _, err := f.ImageRemove(ctx, "sha256:ddd", docker.ImageRemoveOptions{}); !docker.IsConflict(err) {
			t.Errorf("err = %v, want conflict", err)
		}
		if _, err := f.ImageRemove(ctx, "ddd", docker.ImageRemoveOptions{Force: true}); err != nil {
			t.Errorf("forced remove by short ID: %v", err)
		}
	})

	t.Run("tag wins over an ID prefix", func(t *testing.T) {
		f := New()
		f.AddImage(image.Summary{ID: "sha256:cafe0001", RepoTags: []string{"other:1"}})
		f.AddImage(image.Summary{ID: "sha256:0002", RepoTags: []string{"cafe:latest"}})
		resp, err := f.ImageRemove(ctx, "cafe", docker.ImageRemoveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if resp[len(resp)-1].Deleted != "sha256:0002" {
			t.Errorf("removed %+v, want the image tagged cafe:latest", resp)
		}
	})

	t.Run("missing and invalid", func(t *testing.T) {
		f := New()
		if _, err := f.ImageRemove(ctx, "nope:1", docker.ImageRemoveOptions{}); !docker.IsNotFound(err) {
			t.Errorf("missing: err = %v, want not found", err)
		}
		if _, err := f.ImageRemove(ctx, "../../containers/x", docker.ImageRemoveOptions{}); !docker.IsInvalidArgument(err) {
			t.Errorf("invalid: err = %v, want invalid argument", err)
		}
	})
}

func TestImagePruneDangling(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	f.AddImage(image.Summary{ID: "sha256:d1", Size: 100})
	f.AddImage(image.Summary{ID: "sha256:d2", RepoTags: []string{"<none>:<none>"}, Size: 50})
	f.AddImage(image.Summary{ID: "sha256:d3", Size: 70}) // used by a stopped container
	f.AddImage(image.Summary{ID: "sha256:t1", RepoTags: []string{"app:1"}, Size: 10})
	f.AddContainer(exited("old", "sha256:d3", "sha256:d3"))

	report, err := f.ImagePruneDangling(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.SpaceReclaimed != 150 || len(report.ImagesDeleted) != 2 {
		t.Errorf("report = %+v, want 2 images and 150 bytes", report)
	}
	imgs, _ := f.ImageList(ctx)
	var ids []string
	for _, img := range imgs {
		ids = append(ids, img.ID)
	}
	if want := []string{"sha256:d3", "sha256:t1"}; !slices.Equal(ids, want) {
		t.Errorf("remaining = %v, want %v", ids, want)
	}
}

func TestImagePull(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)

	t.Run("success with default tag", func(t *testing.T) {
		f := New()
		f.SetPullScript("redis", PullScript{
			Messages: []jsonstream.Message{{ID: "l1", Status: "Downloading"}},
			Image:    image.Summary{ID: "sha256:redis", Size: 42},
		})
		ps, err := f.ImagePull(ctx, "redis")
		if err != nil {
			t.Fatal(err)
		}
		var statuses []string
		for m, err := range ps.Messages(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			if m.Error != nil {
				t.Fatalf("unexpected error message %v", m.Error.Message)
			}
			statuses = append(statuses, m.Status)
		}
		if len(statuses) != 2 || statuses[0] != "Downloading" {
			t.Errorf("statuses = %q", statuses)
		}
		imgs, _ := f.ImageList(ctx)
		if len(imgs) != 1 || !slices.Equal(imgs[0].RepoTags, []string{"redis:latest"}) {
			t.Errorf("images = %+v", imgs)
		}
	})

	t.Run("registry error", func(t *testing.T) {
		f := New()
		f.SetPullScript("redis:7", PullScript{Err: errors.New("manifest unknown")})
		ps, err := f.ImagePull(ctx, "redis:7")
		if err != nil {
			t.Fatal(err)
		}
		var last jsonstream.Message
		for m, err := range ps.Messages(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			last = m
		}
		if last.Error == nil || last.Error.Message != "manifest unknown" {
			t.Errorf("last message = %+v, want error", last)
		}
		if imgs, _ := f.ImageList(ctx); len(imgs) != 0 {
			t.Errorf("failed pull added images: %+v", imgs)
		}
	})

	t.Run("cancelling the pull context aborts", func(t *testing.T) {
		f := New()
		gate := make(chan struct{})
		f.SetPullScript("busybox", PullScript{Gate: gate})
		pctx, cancel := context.WithCancel(ctx)
		ps, err := f.ImagePull(pctx, "busybox")
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		var last error
		for _, err := range ps.Messages(ctx) { // a different, live context
			last = err
		}
		if !errors.Is(last, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", last)
		}
	})

	t.Run("unscripted", func(t *testing.T) {
		f := New()
		if _, err := f.ImagePull(ctx, "does/not-exist"); !docker.IsNotFound(err) {
			t.Errorf("err = %v, want not found", err)
		}
	})

	t.Run("gate and cancel", func(t *testing.T) {
		f := New()
		gate := make(chan struct{})
		f.SetPullScript("busybox", PullScript{Gate: gate})
		pctx, cancel := context.WithCancel(ctx)
		ps, err := f.ImagePull(pctx, "busybox")
		if err != nil {
			t.Fatal(err)
		}
		errc := make(chan error, 1)
		go func() {
			var last error
			for _, err := range ps.Messages(pctx) {
				last = err
			}
			errc <- last
		}()
		select {
		case err := <-errc:
			t.Fatalf("pull finished before the gate opened: %v", err)
		case <-time.After(20 * time.Millisecond):
		}
		cancel()
		select {
		case err := <-errc:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled", err)
			}
		case <-time.After(waitTimeout):
			t.Fatal("pull did not end after cancel")
		}
		if imgs, _ := f.ImageList(ctx); len(imgs) != 0 {
			t.Errorf("cancelled pull added images: %+v", imgs)
		}
	})
}

func TestVolumes(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()

	v, err := f.VolumeCreate(ctx, "cache-data")
	if err != nil {
		t.Fatal(err)
	}
	if v.Driver != "local" || v.Mountpoint == "" {
		t.Errorf("created volume = %+v", v)
	}
	es := f.Events(ctx)
	again, err := f.VolumeCreate(ctx, "cache-data")
	if err != nil || again.CreatedAt != v.CreatedAt {
		t.Errorf("idempotent create = %+v, %v", again, err)
	}
	anon, err := f.VolumeCreate(ctx, "")
	if err != nil || len(anon.Name) != 64 {
		t.Errorf("generated name = %q, %v", anon.Name, err)
	}
	// Only the generated volume emitted an event; the idempotent create did not.
	if m := nextEvent(t, es); m.Actor.ID != anon.Name {
		t.Errorf("event for %q, want %q", m.Actor.ID, anon.Name)
	}

	if _, err := f.VolumeInspect(ctx, "nope"); !docker.IsNotFound(err) {
		t.Errorf("inspect missing: err = %v, want not found", err)
	}
	if _, err := f.VolumeCreate(ctx, "bad/name"); !docker.IsInvalidArgument(err) {
		t.Errorf("create invalid: err = %v, want invalid argument", err)
	}

	f.AddContainer(exited("db", "pg", "sha256:p", volMount("cache-data")))
	if err := f.VolumeRemove(ctx, "cache-data", docker.VolumeRemoveOptions{Force: true}); !docker.IsConflict(err) {
		t.Errorf("remove in use: err = %v, want conflict", err)
	}
	if err := f.VolumeRemove(ctx, anon.Name, docker.VolumeRemoveOptions{}); err != nil {
		t.Errorf("remove unused: %v", err)
	}
	insp, err := f.VolumeInspect(ctx, "cache-data")
	if err != nil || insp.Volume.Name != "cache-data" || len(insp.Raw) == 0 {
		t.Errorf("inspect = %+v, %v", insp, err)
	}
}

func TestExecProbe(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("alpine", "alpine", "sha256:a"))
	f.SetShells(id, "/bin/sh")

	probe := func(shell string) error {
		execID, err := f.ExecCreate(ctx, id, []string{shell, "-c", "true"})
		if err != nil {
			return err
		}
		if err := f.ExecStart(ctx, execID); err != nil {
			return err
		}
		res, err := f.ExecInspect(ctx, execID)
		if err != nil {
			return err
		}
		if res.Running || res.Pid == 0 || res.ExitCode != 0 {
			return errors.New("probe failed")
		}
		return nil
	}
	if err := probe("/bin/bash"); err == nil {
		t.Error("probe /bin/bash succeeded in a container without bash")
	}
	if err := probe("/bin/sh"); err != nil {
		t.Errorf("probe /bin/sh: %v", err)
	}
	var shells []string
	for _, c := range f.CallsTo(MethodExecCreate) {
		shells = append(shells, c.Args[1].([]string)[0])
	}
	if want := []string{"/bin/bash", "/bin/sh"}; !slices.Equal(shells, want) {
		t.Errorf("probe order = %v, want %v", shells, want)
	}

	stopped := f.AddContainer(exited("old", "alpine", "sha256:a"))
	if _, err := f.ExecCreate(ctx, stopped, []string{"/bin/sh"}); !docker.IsConflict(err) {
		t.Errorf("exec in stopped container: err = %v, want conflict", err)
	}
}

func TestEvents(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	es := f.Events(ctx)

	id := f.AddContainer(running("web", "nginx", "sha256:a"))
	if err := f.ContainerStop(ctx, id); err != nil {
		t.Fatal(err)
	}
	f.AddVolume(volume.Volume{Name: "v"})

	var got []string
	for range 5 { // create, kill, die, stop, volume create
		m := nextEvent(t, es)
		got = append(got, string(m.Type)+"/"+string(m.Action))
	}
	want := []string{"container/create", "container/kill", "container/die", "container/stop", "volume/create"}
	if !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestEventsDoNotBlockEmitters(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	es := f.Events(ctx)
	// Nobody reads yet: emitting many events must not block.
	for range 1000 {
		f.AddVolume(volume.Volume{Name: "v"})
	}
	for range 1000 {
		nextEvent(t, es)
	}
}

func TestDisconnectAndReconnect(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	es := f.Events(ctx)
	f.AddVolume(volume.Volume{Name: "v"}) // queued before the drop

	f.Disconnect(nil)
	if m := nextEvent(t, es); m.Actor.ID != "v" {
		t.Errorf("queued event lost: got %+v", m)
	}
	if err := streamErr(t, es); err == nil || docker.ClassifyConnectError(err) != docker.FailureNotRunning {
		t.Errorf("stream error = %v, want a connection error", err)
	}
	if _, err := f.Ping(ctx); err == nil {
		t.Error("Ping succeeded while disconnected")
	}
	if _, err := f.VolumeList(ctx); err == nil {
		t.Error("VolumeList succeeded while disconnected")
	}
	if err := streamErr(t, f.Events(ctx)); err == nil {
		t.Error("Events succeeded while disconnected")
	}

	f.Reconnect()
	if _, err := f.Ping(ctx); err != nil {
		t.Errorf("Ping after reconnect: %v", err)
	}
	es = f.Events(ctx)
	f.AddVolume(volume.Volume{Name: "w"})
	if m := nextEvent(t, es); m.Actor.ID != "w" {
		t.Errorf("event after reconnect = %+v", m)
	}
}

func TestDropEventsAndCancel(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()

	es := f.Events(ctx)
	f.DropEvents(nil)
	if err := streamErr(t, es); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("drop: err = %v", err)
	}
	if _, err := f.VolumeList(ctx); err != nil {
		t.Errorf("DropEvents disconnected the fake: %v", err)
	}

	cctx, cancel := context.WithCancel(ctx)
	es = f.Events(cctx)
	cancel()
	if err := streamErr(t, es); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: err = %v", err)
	}
	deadline := time.Now().Add(waitTimeout)
	for f.Subscribers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("cancelled subscription was not released")
		}
		time.Sleep(time.Millisecond)
	}
}

// FailFor fails only the named container's calls, even among calls made
// in parallel, and leaves its other methods and other containers alone.
func TestFailForOneContainer(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	refused := errors.New("refused")
	setErr := errors.New("set")
	once := errors.New("once")
	db := f.AddContainer(running("myapp-db-1", "postgres", "sha256:a"))
	var others []string
	for _, n := range []string{"myapp-web-1", "myapp-worker-1", "myapp-cache-1"} {
		others = append(others, f.AddContainer(running(n, "app", "sha256:b")))
	}
	f.FailFor(MethodContainerStop, db, refused)

	ids := append([]string{db}, others...)
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() { errs[i] = f.ContainerStop(ctx, id) })
	}
	wg.Wait()
	if !errors.Is(errs[0], refused) {
		t.Errorf("stop db: err = %v, want %v", errs[0], refused)
	}
	for i, err := range errs[1:] {
		if err != nil {
			t.Errorf("stop %s: %v", others[i], err)
		}
	}
	states := map[string]container.ContainerState{}
	for _, c := range mustList(t, f) {
		states[c.ID] = c.State
	}
	if states[db] != container.StateRunning {
		t.Errorf("refused stop changed db to %s", states[db])
	}
	for _, id := range others {
		if states[id] != container.StateExited {
			t.Errorf("container %s is %s, want exited", id, states[id])
		}
	}
	if n := len(f.CallsTo(MethodContainerStop)); n != len(ids) {
		t.Errorf("recorded %d stop calls, want %d", n, len(ids))
	}

	// Other methods on the same container are unaffected.
	if err := f.ContainerPause(ctx, db); err != nil {
		t.Errorf("pause db: %v", err)
	}
	// Precedence: FailNext, then FailFor, then SetError.
	f.SetError(MethodContainerStop, setErr)
	f.FailNext(MethodContainerStop, once)
	if err := f.ContainerStop(ctx, db); !errors.Is(err, once) {
		t.Errorf("with FailNext queued: err = %v, want %v", err, once)
	}
	if err := f.ContainerStop(ctx, db); !errors.Is(err, refused) {
		t.Errorf("FailFor over SetError: err = %v, want %v", err, refused)
	}
	if err := f.ContainerStop(ctx, others[0]); !errors.Is(err, setErr) {
		t.Errorf("other container with SetError: err = %v, want %v", err, setErr)
	}
	f.SetError(MethodContainerStop, nil)

	// Clearing with a nil error lets the call through.
	f.FailFor(MethodContainerStop, db, nil)
	if err := f.ContainerStop(ctx, db); err != nil {
		t.Errorf("after clearing: %v", err)
	}
	if st := mustList(t, f)[0].State; st != container.StateExited {
		t.Errorf("db is %s after a stop with FailFor cleared, want exited", st)
	}
}

func mustList(t *testing.T, f *Service) []container.Summary {
	t.Helper()
	cs, err := f.ContainerList(ctxT(t), docker.ContainerListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestComposeLabels(t *testing.T) {
	t.Parallel()
	got := Compose{Project: "myapp", Service: "web"}.Labels()
	want := map[string]string{
		ComposeProjectLabel: "myapp", ComposeServiceLabel: "web", ComposeOneoffLabel: "False",
	}
	if !maps.Equal(got, want) {
		t.Errorf("Labels() = %v, want %v", got, want)
	}
	got = Compose{Project: "myapp", Service: "web", WorkingDir: "/srv/myapp", OneOff: true}.Labels()
	if got[ComposeOneoffLabel] != "True" || got[ComposeWorkingDirLabel] != "/srv/myapp" {
		t.Errorf("one-off Labels() = %v", got)
	}
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(container.Summary{Names: []string{"/myapp-web-1"}, Labels: got})
	got["mutated"] = "x"
	res, err := f.ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if l := res.Container.Config.Labels; l[ComposeProjectLabel] != "myapp" || l["mutated"] != "" {
		t.Errorf("stored labels = %v", l)
	}
}

func TestErrorInjection(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	boom := errors.New("boom")
	once := errors.New("once")

	f.SetError(MethodVolumeList, boom)
	f.FailNext(MethodVolumeList, once)
	if _, err := f.VolumeList(ctx); !errors.Is(err, once) {
		t.Errorf("first call: err = %v, want %v", err, once)
	}
	if _, err := f.VolumeList(ctx); !errors.Is(err, boom) {
		t.Errorf("second call: err = %v, want %v", err, boom)
	}
	f.SetError(MethodVolumeList, nil)
	if _, err := f.VolumeList(ctx); err != nil {
		t.Errorf("after clearing: %v", err)
	}
	if n := len(f.CallsTo(MethodVolumeList)); n != 3 {
		t.Errorf("recorded %d calls, want 3", n)
	}

	f.FailNext(MethodEvents, boom)
	if err := streamErr(t, f.Events(ctx)); !errors.Is(err, boom) {
		t.Errorf("events: err = %v", err)
	}
	f.ResetCalls()
	if len(f.Calls()) != 0 {
		t.Error("ResetCalls kept calls")
	}
}

func TestPingNegotiatesDown(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	f.SetEngineAPIVersion("1.44")
	res, err := f.Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.APIVersion != "1.44" || f.APIVersion() != "1.44" {
		t.Errorf("ping = %+v, client version %s; want 1.44", res, f.APIVersion())
	}
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	es := f.Events(ctx)
	go func() {
		for range es.Messages {
		}
	}()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 50 {
				id := f.AddContainer(running("c", "img", "sha256:x"))
				_ = f.ContainerStop(ctx, id)
				_, _ = f.ContainerList(ctx, docker.ContainerListOptions{All: true})
				_, _ = f.VolumeCreate(ctx, "")
				_, _ = f.ImageList(ctx)
				f.WriteLog(id, i%2 == 0, string(rune('a'+i))+"\n")
			}
		})
	}
	wg.Wait()
}

func statsSample(total uint64) container.StatsResponse {
	return container.StatsResponse{
		CPUStats:    container.CPUStats{CPUUsage: container.CPUUsage{TotalUsage: total, PercpuUsage: []uint64{total}}},
		MemoryStats: container.MemoryStats{Usage: total, Stats: map[string]uint64{"inactive_file": 1}},
	}
}

// statsResult is the outcome of iterating a stats stream in the background.
type statsResult struct {
	samples chan container.StatsResponse
	end     chan error // receives the final error (nil for a clean end)
}

func iterateStats(ctx context.Context, st docker.StatsStream) statsResult {
	r := statsResult{samples: make(chan container.StatsResponse, 16), end: make(chan error, 1)}
	go func() {
		for s, err := range st.Samples(ctx) {
			if err != nil {
				r.end <- err
				return
			}
			r.samples <- s
		}
		r.end <- nil
	}()
	return r
}

func (r statsResult) next(t *testing.T) container.StatsResponse {
	t.Helper()
	select {
	case s := <-r.samples:
		return s
	case err := <-r.end:
		// The iterator sends every sample before the end, so a sample may
		// be pending although select picked the end.
		select {
		case s := <-r.samples:
			r.end <- err // put the end back for a later wait
			return s
		default:
		}
		t.Fatalf("stats stream ended: %v", err)
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for a stats sample")
	}
	return container.StatsResponse{}
}

func (r statsResult) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-r.end:
		return err
	case s := <-r.samples:
		t.Fatalf("unexpected sample %+v", s)
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for the stats stream to end")
	}
	return nil
}

func waitStatsStreams(t *testing.T, f *Service, want int) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for f.StatsStreams() != want {
		if time.Now().After(deadline) {
			t.Fatalf("StatsStreams() = %d, want %d", f.StatsStreams(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestContainerStatsYieldsQueuedSamplesInOrder(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:n"))
	f.SetStats(id, statsSample(1), statsSample(2)) // queued before the stream opens

	st, err := f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatalf("ContainerStats: %v", err)
	}
	defer func() { _ = st.Close() }()
	r := iterateStats(ctx, st)
	for _, want := range []uint64{1, 2} {
		if got := r.next(t).CPUStats.CPUUsage.TotalUsage; got != want {
			t.Errorf("sample TotalUsage = %d, want %d", got, want)
		}
	}
	f.SetStats("web", statsSample(3)) // by name, while the stream waits
	if got := r.next(t).CPUStats.CPUUsage.TotalUsage; got != 3 {
		t.Errorf("sample TotalUsage = %d, want 3", got)
	}
	if n := len(f.CallsTo(MethodContainerStats)); n != 1 {
		t.Errorf("recorded %d ContainerStats calls, want 1", n)
	}
}

func TestContainerStatsSamplesAreCopied(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:n"))
	sample := statsSample(1)
	f.SetStats(id, sample)
	sample.CPUStats.CPUUsage.PercpuUsage[0] = 99
	sample.MemoryStats.Stats["inactive_file"] = 99

	st, err := f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	got := iterateStats(ctx, st).next(t)
	if got.CPUStats.CPUUsage.PercpuUsage[0] != 1 || got.MemoryStats.Stats["inactive_file"] != 1 {
		t.Errorf("queued sample shares memory with the caller: %+v", got)
	}
}

func TestContainerStatsCount(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:n"))

	st1, err := f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithCancel(ctx)
	if _, err := f.ContainerStats(sctx, id); err != nil {
		t.Fatal(err)
	}
	if n := f.StatsStreams(); n != 2 {
		t.Fatalf("StatsStreams() = %d after opening 2, want 2", n)
	}

	if err := st1.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := st1.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if n := f.StatsStreams(); n != 1 {
		t.Errorf("StatsStreams() = %d after Close, want 1", n)
	}
	if err := iterateStats(ctx, st1).wait(t); !errors.Is(err, docker.ErrStatsStreamClosed) {
		t.Errorf("iterating a closed stream: err = %v, want ErrStatsStreamClosed", err)
	}

	cancel() // never iterated: cancelling ctx alone releases it
	waitStatsStreams(t, f, 0)
}

func TestContainerStatsCancelEndsIteration(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:n"))
	st, err := f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	ictx, cancel := context.WithCancel(ctx)
	r := iterateStats(ictx, st)
	cancel()
	if err := r.wait(t); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	waitStatsStreams(t, f, 0)
}

func TestContainerStatsDisconnect(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:n"))
	st, err := f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r := iterateStats(ctx, st)
	f.Disconnect(nil)
	if err := r.wait(t); err == nil || docker.ClassifyConnectError(err) != docker.FailureNotRunning {
		t.Errorf("stream error = %v, want a connection error", err)
	}
	waitStatsStreams(t, f, 0)
	if _, err := f.ContainerStats(ctx, id); err == nil {
		t.Error("ContainerStats succeeded while disconnected")
	}

	f.Reconnect()
	st, err = f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatalf("ContainerStats after reconnect: %v", err)
	}
	defer func() { _ = st.Close() }()
	f.SetStats(id, statsSample(7))
	if got := iterateStats(ctx, st).next(t).CPUStats.CPUUsage.TotalUsage; got != 7 {
		t.Errorf("sample after reconnect = %d, want 7", got)
	}
}

func TestContainerStatsEndsWhenContainerStops(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	stop := func(id string) { _ = f.ContainerStop(ctx, id) }
	remove := func(id string) {
		_ = f.ContainerRemove(ctx, id, docker.ContainerRemoveOptions{Force: true})
	}
	external := func(id string) { f.RemoveContainer(id) }
	replace := func(id string) {
		c := exited("web", "nginx", "sha256:n")
		c.ID = id
		f.AddContainer(c)
	}
	for name, end := range map[string]func(string){
		"stop": stop, "remove": remove, "external remove": external, "replaced by stopped": replace,
	} {
		t.Run(name, func(t *testing.T) {
			id := f.AddContainer(running("web-"+name, "nginx", "sha256:n"))
			other := f.AddContainer(running("other-"+name, "nginx", "sha256:n"))
			st, err := f.ContainerStats(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			ost, err := f.ContainerStats(ctx, other)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ost.Close() }()
			r := iterateStats(ctx, st)
			end(id)
			if err := r.wait(t); err != nil {
				t.Errorf("stream ended with %v, want a clean end", err)
			}
			// Other containers' streams keep running.
			f.SetStats(other, statsSample(5))
			if got := iterateStats(ctx, ost).next(t).CPUStats.CPUUsage.TotalUsage; got != 5 {
				t.Errorf("other stream sample = %d, want 5", got)
			}
		})
	}
	waitStatsStreams(t, f, 0)
}

func TestContainerStatsErrors(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	stopped := f.AddContainer(exited("old", "nginx", "sha256:n"))
	paused := running("paused", "nginx", "sha256:n")
	paused.State = container.StatePaused
	pausedID := f.AddContainer(paused)
	web := f.AddContainer(running("web", "nginx", "sha256:n"))

	if _, err := f.ContainerStats(ctx, "missing"); !docker.IsNotFound(err) {
		t.Errorf("unknown container: err = %v, want not found", err)
	}
	if _, err := f.ContainerStats(ctx, "../images/x"); !docker.IsInvalidArgument(err) {
		t.Errorf("invalid ID: err = %v, want invalid argument", err)
	}
	for _, id := range []string{stopped, pausedID} {
		if _, err := f.ContainerStats(ctx, id); !docker.IsConflict(err) {
			t.Errorf("non-running %s: err = %v, want conflict", id[:12], err)
		}
	}
	boom := errors.New("boom")
	f.FailNext(MethodContainerStats, boom)
	if _, err := f.ContainerStats(ctx, web); !errors.Is(err, boom) {
		t.Errorf("injected: err = %v, want %v", err, boom)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.ContainerStats(cctx, web); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: err = %v", err)
	}
	if n := f.StatsStreams(); n != 0 {
		t.Errorf("failed opens left %d streams open", n)
	}
}

func TestContainerStatsCloseEndsService(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:n"))
	st, err := f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	r := iterateStats(ctx, st)
	_ = f.Close()
	if err := r.wait(t); err == nil {
		t.Error("closing the service ended the stream without an error")
	}
	waitStatsStreams(t, f, 0)
}

func TestContainerStatsConcurrentUse(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = f.AddContainer(running("c"+string(rune('a'+i)), "img", "sha256:x"))
	}
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			for range 20 {
				sctx, cancel := context.WithCancel(ctx)
				st, err := f.ContainerStats(sctx, id)
				if err != nil {
					cancel()
					continue // the container may have been stopped below
				}
				f.SetStats(id, statsSample(1))
				for _, err := range st.Samples(sctx) {
					_ = err
					break
				}
				cancel()
			}
			if i%2 == 0 {
				_ = f.ContainerStop(ctx, id)
			}
		})
	}
	wg.Wait()
	waitStatsStreams(t, f, 0)
}

func TestContainerStatsStopDeliversQueuedSamplesThenEnds(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:n"))
	st, err := f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// Queue and stop before anyone reads: the outcome must not depend on
	// goroutine timing.
	f.SetStats(id, statsSample(1), statsSample(2))
	if err := f.ContainerStop(ctx, id); err != nil {
		t.Fatal(err)
	}
	r := iterateStats(ctx, st)
	for _, want := range []uint64{1, 2} {
		if got := r.next(t).CPUStats.CPUUsage.TotalUsage; got != want {
			t.Errorf("sample TotalUsage = %d, want %d", got, want)
		}
	}
	if err := r.wait(t); err != nil {
		t.Errorf("stream ended with %v, want a clean end", err)
	}

	// Nothing stale survives into the next run of the container.
	if err := f.ContainerStart(ctx, id); err != nil {
		t.Fatal(err)
	}
	st, err = f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	r = iterateStats(ctx, st)
	f.SetStats(id, statsSample(9))
	if got := r.next(t).CPUStats.CPUUsage.TotalUsage; got != 9 {
		t.Errorf("first sample after restart = %d, want 9 (a stale sample leaked)", got)
	}
}

func TestContainerStatsDisconnectDropsQueuedSamples(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := f.AddContainer(running("web", "nginx", "sha256:n"))
	st, err := f.ContainerStats(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	f.SetStats(id, statsSample(1))
	f.Disconnect(nil)
	if err := iterateStats(ctx, st).wait(t); err == nil {
		t.Error("a lost connection ended the stream without an error")
	}
}

func TestContainerInspectHealth(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	tests := []struct {
		name   string
		health *container.HealthSummary
		want   container.HealthStatus // "" for no State.Health
	}{
		{"no health", nil, ""},
		{"no healthcheck", &container.HealthSummary{Status: container.NoHealthcheck}, ""},
		{"healthy", &container.HealthSummary{Status: container.Healthy, FailingStreak: 0}, container.Healthy},
		{"unhealthy", &container.HealthSummary{Status: container.Unhealthy, FailingStreak: 3}, container.Unhealthy},
	}
	for i, tt := range tests {
		id := f.AddContainer(container.Summary{ID: strings.Repeat(string(rune('a'+i)), 64), Names: []string{"/c"}, State: container.StateRunning, Health: tt.health})
		res, err := f.ContainerInspect(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		h := res.Container.State.Health
		switch {
		case tt.want == "" && h != nil:
			t.Errorf("%s: State.Health = %+v, want none", tt.name, h)
		case tt.want != "" && (h == nil || h.Status != tt.want || h.FailingStreak != tt.health.FailingStreak):
			t.Errorf("%s: State.Health = %+v, want %s with streak %d", tt.name, h, tt.want, tt.health.FailingStreak)
		}
		if tt.want != "" && !bytes.Contains(res.Raw, []byte(`"Health":{"Status":"`+string(tt.want)+`"`)) {
			t.Errorf("%s: raw inspection lacks the health status: %s", tt.name, res.Raw)
		}
	}
}
