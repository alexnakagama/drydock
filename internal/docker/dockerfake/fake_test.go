package dockerfake

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

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
	defer rc.Close()
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
			rc.Close()
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
