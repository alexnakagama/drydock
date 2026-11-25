package views

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
)

// imgID returns a full image ID made of one repeated hex digit.
func imgID(digit byte) string { return "sha256:" + strings.Repeat(string(digit), 64) }

// img builds an image summary as the engine lists it, created age ago.
func img(id string, size int64, age time.Duration, tags ...string) image.Summary {
	return image.Summary{ID: id, RepoTags: tags, Size: size, Created: testNow.Add(-age).Unix()}
}

// user builds a container created from the image with the given ID.
func user(n int, name, imageRef, imageID string, state container.ContainerState) container.Summary {
	c := ctr(cid(n), name, imageRef, state, string(state))
	c.ImageID = imageID
	return c
}

const day = 24 * time.Hour

// imageSample returns a fake engine with:
//   - nginx (tags nginx:1.27 and nginx:latest), used by web (running) and
//     old (stopped);
//   - app (tags app:latest and app:1.0), unused;
//   - alpine:3.20, unused;
//   - two unused dangling images, 300MB and 150MB, one reported with the
//     "<none>:<none>" placeholder of older engines;
//   - a dangling image used by the stopped container builder.
func imageSample() *dockerfake.Service {
	f := dockerfake.New()
	f.AddImage(img(imgID('a'), 187_000_000, 14*day, "nginx:1.27", "nginx:latest"))
	f.AddImage(img(imgID('b'), 52_300_000, 3*day, "app:latest", "app:1.0"))
	f.AddImage(img(imgID('c'), 7_800_000, 60*day, "alpine:3.20"))
	f.AddImage(img(imgID('d'), 300_000_000, 2*day))
	f.AddImage(img(imgID('e'), 150_000_000, 5*day, "<none>:<none>"))
	f.AddImage(img(imgID('f'), 90_000_000, 7*day))
	f.AddContainer(user(0, "web", "nginx:1.27", imgID('a'), container.StateRunning))
	f.AddContainer(user(1, "old", "nginx:1.27", imgID('a'), container.StateExited))
	f.AddContainer(user(2, "builder", imgID('f'), imgID('f'), container.StateExited))
	return f
}

// imagesEnv starts an env on the given engine and switches to the images
// view.
func imagesEnv(t *testing.T, f *dockerfake.Service, width, height int) *env {
	t.Helper()
	e := newEnv(t, f, width, height)
	e.command("images")
	if !strings.HasPrefix(e.header(), "drydock > images") {
		t.Fatalf("images view not shown; screen:\n%s", e.screen())
	}
	return e
}

func (e *env) imagesView() *imagesView { return e.images[len(e.images)-1] }

// selectImage moves the selection of the images view to the row labelled
// label.
func (e *env) selectImage(label string) {
	e.t.Helper()
	v := e.imagesView()
	for k, r := range v.shown {
		if r.label == label && v.table.Select(k) {
			return
		}
	}
	e.t.Fatalf("no image row %s; screen:\n%s", label, e.screen())
}

// labels returns the REPOSITORY:TAG cells listed, in order.
func (e *env) labels() []string {
	var out []string
	for _, l := range e.content()[1:] {
		if f := rowFields(l); len(f) > 0 && strings.Contains(f[0], ":") {
			out = append(out, f[0])
		}
	}
	return out
}

func TestImagesGolden(t *testing.T) {
	e := imagesEnv(t, imageSample(), 100, 12)
	checkGolden(t, "images_tagged_100x12.golden", e.screen())

	e.press("a")
	checkGolden(t, "images_all_100x12.golden", e.screen())
}

func TestImagesOneRowPerTag(t *testing.T) {
	e := imagesEnv(t, imageSample(), 100, 14)
	want := []string{"alpine:3.20", "app:1.0", "app:latest", "nginx:1.27", "nginx:latest"}
	if got := e.labels(); !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	// Both tags of one image show the same short ID and the same users.
	for _, tag := range []string{"nginx:1.27", "nginx:latest"} {
		f := rowFields(e.rowOf(tag))
		if len(f) < 2 || f[1] != strings.Repeat("a", 12) {
			t.Errorf("%s row %q, want ID %s", tag, e.rowOf(tag), strings.Repeat("a", 12))
		}
		// IN USE counts the running and the stopped container.
		if f[len(f)-1] != "2" {
			t.Errorf("%s row %q, want IN USE 2", tag, e.rowOf(tag))
		}
	}
	if f := rowFields(e.rowOf("alpine:3.20")); f[len(f)-1] != "0" {
		t.Errorf("alpine row %q, want IN USE 0", e.rowOf("alpine:3.20"))
	}
	row := e.rowOf("nginx:1.27")
	if !strings.Contains(row, "187MB") || !strings.Contains(row, "2 weeks ago") {
		t.Errorf("nginx row %q, want human size and relative age", row)
	}
}

func TestImagesDanglingToggle(t *testing.T) {
	e := imagesEnv(t, imageSample(), 100, 14)
	if strings.Contains(e.screen(), "<none>:<none>") {
		t.Fatalf("dangling images shown by default:\n%s", e.screen())
	}
	if !strings.Contains(e.header(), "[tagged]") {
		t.Errorf("header %q, want the tagged indicator", e.header())
	}
	e.press("a")
	if n := strings.Count(e.screen(), "<none>:<none>"); n != 3 {
		t.Errorf("%d dangling rows in all mode, want 3:\n%s", n, e.screen())
	}
	if !strings.Contains(e.header(), "[all]") {
		t.Errorf("header %q, want the all indicator", e.header())
	}
	// Dangling rows come after the tagged ones.
	ls := e.labels()
	if ls[len(ls)-1] != "<none>:<none>" || ls[0] == "<none>:<none>" {
		t.Errorf("row order %v", ls)
	}
	e.press("a")
	if strings.Contains(e.screen(), "<none>:<none>") {
		t.Errorf("dangling rows after toggling back:\n%s", e.screen())
	}
}

func TestImagesEmptyStates(t *testing.T) {
	e := imagesEnv(t, dockerfake.New(), 100, 8)
	if !strings.Contains(e.screen(), "No images.") {
		t.Errorf("empty engine:\n%s", e.screen())
	}
	f := dockerfake.New()
	f.AddImage(img(imgID('d'), 1000, day))
	e = imagesEnv(t, f, 100, 8)
	if !strings.Contains(e.screen(), "No tagged images (1 dangling; press a to show all).") {
		t.Errorf("only dangling images:\n%s", e.screen())
	}
}

func digestRef(repo string, digit byte) string { return repo + "@" + imgID(digit) }

func TestImageRowsDigestOnly(t *testing.T) {
	t.Parallel()
	// All digests in one repository: one row, removed by ID so that no
	// digest is left behind.
	im := image.Summary{ID: imgID('a'), RepoDigests: []string{digestRef("nginx", '1'), digestRef("nginx", '2'), noneDigest}}
	rows := imageRows(im)
	if len(rows) != 1 || rows[0].label != "nginx:<none>" || rows[0].dangling || rows[0].tag || rows[0].ref != im.ID {
		t.Fatalf("rows %+v, want one nginx:<none> row addressed by ID", rows)
	}
	if isDanglingImage(im) {
		t.Error("an image with a digest is not dangling")
	}

	// Several repositories: one row per repository, addressed by digest.
	im = image.Summary{ID: imgID('b'), RepoDigests: []string{digestRef("nginx", '1'), digestRef("mirror.local/nginx", '1')}}
	rows = imageRows(im)
	if len(rows) != 2 || rows[0].ref != im.RepoDigests[0] || rows[1].ref != im.RepoDigests[1] ||
		rows[1].label != "mirror.local/nginx:<none>" {
		t.Fatalf("rows %+v, want one row per repository addressed by digest", rows)
	}

	if !isDanglingImage(image.Summary{ID: imgID('c'), RepoTags: []string{noneTag}, RepoDigests: []string{noneDigest}}) {
		t.Error("placeholders only: dangling")
	}
}

func TestRemainingRefs(t *testing.T) {
	t.Parallel()
	row := func(ref string, tags, digests []string) imageRow {
		im := image.Summary{ID: imgID('a'), RepoTags: tags, RepoDigests: digests}
		for _, r := range imageRows(im) {
			if r.ref == ref {
				return r
			}
		}
		t.Fatalf("no row for %s in %v", ref, imageRows(im))
		return imageRow{}
	}
	nginxD, mirrorD := digestRef("nginx", '1'), digestRef("mirror.local/app", '2')
	tests := []struct {
		name string
		r    imageRow
		want []string
	}{
		{"only tag", row("nginx:1.27", []string{"nginx:1.27"}, nil), nil},
		{"only tag, same-repo digest goes with it", row("nginx:1.27", []string{"nginx:1.27"}, []string{nginxD}), nil},
		{"other tag of the same repo keeps digests", row("nginx:1.27", []string{"nginx:1.27", "nginx:latest"}, []string{nginxD}),
			[]string{"nginx:latest", nginxD}},
		{"other repository's digest keeps the image", row("app:1", []string{"app:1"}, []string{mirrorD}), []string{mirrorD}},
		{"tag of another repository", row("app:1", []string{"app:1", "nginx:x"}, []string{nginxD}), []string{"nginx:x", nginxD}},
		{"one digest of several repositories", row(nginxD, nil, []string{nginxD, mirrorD}), []string{mirrorD}},
		{"dangling, by ID", row(imgID('a'), nil, nil), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := remainingRefs(tt.r); !slices.Equal(got, tt.want) {
				t.Errorf("remainingRefs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeleteInUseTagKeptByOtherRepositoryDigest(t *testing.T) {
	f := dockerfake.New()
	im := img(imgID('a'), 1000, day, "app:1")
	im.RepoDigests = []string{digestRef("mirror.local/app", '2')}
	f.AddImage(im)
	f.AddContainer(user(0, "web", "app:1", imgID('a'), container.StateRunning))
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("app:1")
	e.press("d")
	e.settle()
	d := e.dialogText()
	if !strings.Contains(d, "Only the tag app:1 is removed.") ||
		!strings.Contains(d, "The image stays referenced as mirror.local/app@222222222222.") || strings.Contains(d, "refuse") {
		t.Errorf("confirmation:\n%s", e.screen())
	}
}

func TestDeleteRefusedWhenReferencesChanged(t *testing.T) {
	f := dockerfake.New()
	f.AddImage(img(imgID('a'), 1000, day, "app:1", "app:2"))
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("app:1")
	e.press("d")
	e.settle()
	if !strings.Contains(e.screen(), "Only the tag app:1 is removed.") {
		t.Fatalf("confirmation:\n%s", e.screen())
	}
	// While the dialog is open, the other tag is removed elsewhere: the
	// untag would now delete the image.
	if _, err := f.ImageRemove(context.Background(), "app:2", docker.ImageRemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	f.ResetCalls()
	e.press("y")
	e.waitStatus("the references of app:1 changed; nothing was removed")
	if c := removeCalls(f); len(c) != 0 {
		t.Errorf("ImageRemove called: %v", c)
	}
}

func TestDeleteRefusedWhenTagMoved(t *testing.T) {
	f := dockerfake.New()
	f.AddImage(img(imgID('a'), 187_000_000, day, "nginx:latest"))
	f.AddContainer(user(0, "web", "nginx:latest", imgID('a'), container.StateRunning))
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("nginx:latest")
	e.press("d")
	e.settle()
	if !strings.Contains(e.screen(), "the engine will refuse to remove it") {
		t.Fatalf("confirmation:\n%s", e.screen())
	}
	// While the dialog is open, a pull elsewhere moves the tag to a new,
	// unused image.
	f.SetPullScript("nginx:latest", dockerfake.PullScript{Image: img(imgID('b'), 1000, 0)})
	stream, err := f.ImagePull(context.Background(), "nginx:latest")
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range stream.Messages(context.Background()) {
		if err != nil {
			t.Fatal(err)
		}
	}
	e.press("y")
	e.waitStatus("nginx:latest now refers to a different image; nothing was removed")
	if c := removeCalls(f); len(c) != 0 {
		t.Errorf("ImageRemove called for a moved tag: %v", c)
	}
}

func TestImagesViewNeutralizesHostileText(t *testing.T) {
	hostile := []rune{0x1b, 0x9b, 0x202e, 0x07}
	f := dockerfake.New()
	tag := "evil/app:1\x1b]52;c;cHduZWQ=\a\u202e\u009b31m"
	f.AddImage(img(imgID('a'), 1000, day, tag))
	f.AddContainer(user(0, "web\x1b[2J", tag, imgID('a'), container.StateRunning))
	f.SetPullScript("evil/app:2", dockerfake.PullScript{Err: errors.New("denied\x1b[2J\u009b31m\u202e")})
	e := imagesEnv(t, f, 100, 20)

	check := func(step string) {
		t.Helper()
		raw := e.m.View().Content
		// Styling uses ESC-based CSI sequences, so for ESC look for the
		// injected payloads; every other rune must not appear at all.
		// Runes are compared, not bytes: 0x9b is also a UTF-8
		// continuation byte of legitimate glyphs.
		if strings.Contains(raw, "\x1b]52") || strings.Contains(raw, "\x1b[2J") {
			t.Errorf("%s: injected escape sequence reached the screen", step)
		}
		for _, h := range hostile[1:] {
			if strings.ContainsRune(raw, h) {
				t.Errorf("%s: %U reached the screen", step, h)
			}
		}
	}
	check("list")
	// The only row is selected.
	e.press("d")
	e.settle()
	check("delete dialog")
	e.press("n")
	e.press("enter")
	e.settle()
	check("drill-down")
	e.press("esc")
	e.press("i")
	e.until("inspect loaded", func() bool { return strings.Contains(e.screen(), `"RepoTags"`) })
	check("inspect")
	e.press("esc")
	e.press("p")
	e.settle()
	e.typeText("evil/app:2")
	e.press("enter")
	e.waitStatus("pull evil/app:2: denied")
	check("pull error")
}

func TestPullRefusesLocalImageID(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 20)
	for _, in := range []string{"cccccccccccc", "sha256:cccccccccccccccc", imgID('c')} {
		e.press("p")
		e.settle()
		e.typeText(in)
		e.press("enter")
		e.waitStatus("pull: that is")
		e.settle()
	}
	if c := pullCalls(f); len(c) != 0 {
		t.Errorf("local image ID sent to a registry: %v", c)
	}
}

func TestImagesSelectionSurvivesRefresh(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 14)
	e.selectImage("nginx:latest")
	f.AddImage(img(imgID('9'), 1000, day, "aaa:1"))
	e.until("new image listed", func() bool { return e.rowOf("aaa:1") != "" })
	if r, _ := e.imagesView().selected(); r.label != "nginx:latest" {
		t.Errorf("selection moved to %q", r.label)
	}
}

func removeCalls(f *dockerfake.Service) []dockerfake.Call {
	return f.CallsTo(dockerfake.MethodImageRemove)
}

func wantRemove(t *testing.T, f *dockerfake.Service, ref string) {
	t.Helper()
	calls := removeCalls(f)
	if len(calls) != 1 {
		t.Fatalf("%d ImageRemove calls, want 1", len(calls))
	}
	want := docker.ImageRemoveOptions{Force: false, PruneChildren: true}
	if calls[0].Args[0] != ref || calls[0].Args[1] != want {
		t.Errorf("ImageRemove%v, want (%s, %+v)", calls[0].Args, ref, want)
	}
}

func TestDeleteUnusedImage(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("alpine:3.20")
	e.press("d")
	e.settle()
	s := e.screen()
	if !strings.Contains(s, "Delete image alpine:3.20?") || !strings.Contains(s, "Image cccccccccccc (7.8MB) will be removed") {
		t.Fatalf("confirmation:\n%s", s)
	}
	e.press("y")
	e.waitStatus("delete alpine:3.20: done")
	e.until("row removed", func() bool { return e.rowOf("alpine:3.20") == "" })
	wantRemove(t, f, "alpine:3.20")
}

func TestDeleteSharedTagRemovesOnlyThatTag(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("app:1.0")
	e.press("d")
	e.settle()
	if s := e.screen(); !strings.Contains(s, "Only the tag app:1.0 is removed.") || !strings.Contains(s, "The image stays tagged as app:latest.") {
		t.Fatalf("confirmation:\n%s", s)
	}
	e.press("y")
	e.waitStatus("delete app:1.0: done")
	e.until("tag removed", func() bool { return e.rowOf("app:1.0") == "" })
	if e.rowOf("app:latest") == "" {
		t.Errorf("app:latest no longer listed:\n%s", e.screen())
	}
	wantRemove(t, f, "app:1.0")
}

func TestUntagInUseImageWithOtherTags(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("nginx:1.27")
	e.press("d")
	e.settle()
	s := e.screen()
	if !strings.Contains(s, "Only the tag nginx:1.27 is removed.") || !strings.Contains(s, "The image stays tagged as nginx:latest.") {
		t.Fatalf("confirmation:\n%s", s)
	}
	if strings.Contains(s, "refuse") {
		t.Errorf("untagging is not refused, but the dialog warns it is:\n%s", s)
	}
	e.press("y")
	e.waitStatus("delete nginx:1.27: done")
	e.until("tag removed", func() bool { return e.rowOf("nginx:1.27") == "" })
	if row := e.rowOf("nginx:latest"); row == "" || !strings.HasSuffix(row, "2") {
		t.Errorf("nginx:latest row %q, want it listed and still in use by 2", row)
	}
	wantRemove(t, f, "nginx:1.27")
}

func TestDeleteInUseImageRefused(t *testing.T) {
	f := dockerfake.New()
	f.AddImage(img(imgID('a'), 187_000_000, day, "nginx:1.27"))
	f.AddContainer(user(0, "web", "nginx:1.27", imgID('a'), container.StateRunning))
	f.AddContainer(user(1, "old", "nginx:1.27", imgID('a'), container.StateExited))
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("nginx:1.27")
	e.press("d")
	e.settle()
	s := e.screen()
	for _, want := range []string{"nginx:1.27 is used by 2 containers;", "the engine will refuse to remove it:", "old (exited)", "web (running)"} {
		if !strings.Contains(s, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, s)
		}
	}
	e.press("y")
	e.waitStatus("delete nginx:1.27: conflict: unable to remove repository reference")
	if e.rowOf("nginx:1.27") == "" {
		t.Errorf("image no longer listed:\n%s", e.screen())
	}
	wantRemove(t, f, "nginx:1.27")
}

func TestDeleteDanglingImageByID(t *testing.T) {
	f := dockerfake.New()
	f.AddImage(img(imgID('d'), 1000, day))
	e := imagesEnv(t, f, 100, 20)
	e.press("a")
	e.selectImage(noneTag)
	e.press("d")
	e.settle()
	if s := e.screen(); !strings.Contains(s, "Delete image dddddddddddd?") {
		t.Fatalf("confirmation:\n%s", s)
	}
	e.press("y")
	e.waitStatus("delete dddddddddddd: done")
	wantRemove(t, f, imgID('d'))
}

func TestDeleteImageCancelled(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("alpine:3.20")
	e.press("d")
	e.settle()
	e.press("n")
	e.settle()
	if len(removeCalls(f)) != 0 || e.rowOf("alpine:3.20") == "" {
		t.Errorf("cancelled delete removed the image")
	}
}

func TestInspectImage(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("nginx:1.27")
	e.press("i")
	e.until("inspect loaded", func() bool { return strings.Contains(e.screen(), `"RepoTags"`) })
	if h := e.header(); !strings.HasPrefix(h, "drydock > images > nginx:1.27 > inspect") {
		t.Errorf("header %q", h)
	}
	// Inspected by ID, so the view shows the image the row was listing.
	calls := f.CallsTo(dockerfake.MethodImageInspect)
	if len(calls) != 1 || calls[0].Args[0] != imgID('a') {
		t.Errorf("ImageInspect calls %v, want one by ID", calls)
	}
	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > images") || strings.Contains(h, "inspect") {
		t.Errorf("header after esc %q", h)
	}
}

func TestImageDrillDownToContainers(t *testing.T) {
	f := imageSample()
	f.AddContainer(user(5, "other", "alpine:3.20", imgID('c'), container.StateRunning))
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("nginx:1.27")
	e.press("enter")
	e.settle()
	if h := e.header(); !strings.HasPrefix(h, "drydock > images > nginx:1.27 > containers [all]") {
		t.Fatalf("header %q", h)
	}
	if e.rowOf("web") == "" || e.rowOf("old") == "" || e.rowOf("other") != "" || e.rowOf("builder") != "" {
		t.Errorf("drill-down lists the wrong containers:\n%s", e.screen())
	}
	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > images [tagged]") {
		t.Errorf("header after esc %q", h)
	}
	if r, _ := e.imagesView().selected(); r.label != "nginx:1.27" {
		t.Errorf("selection after esc %q", r.label)
	}
}

func pullCalls(f *dockerfake.Service) []dockerfake.Call { return f.CallsTo(dockerfake.MethodImagePull) }

func layerMsgs(ids ...string) []jsonstream.Message {
	msgs := []jsonstream.Message{{ID: "7", Status: "Pulling from library/redis"}}
	for _, id := range ids {
		msgs = append(msgs, jsonstream.Message{ID: id, Status: "Pulling fs layer"})
	}
	msgs = append(msgs, jsonstream.Message{ID: ids[0], Status: "Downloading"},
		jsonstream.Message{ID: ids[0], Status: "Pull complete"})
	return msgs
}

func TestPullImage(t *testing.T) {
	f := imageSample()
	gate := make(chan struct{})
	f.SetPullScript("redis:7", dockerfake.PullScript{
		Messages: layerMsgs("l1", "l2", "l3"),
		Gate:     gate,
		Image:    img(imgID('7'), 117_000_000, day),
	})
	e := imagesEnv(t, f, 100, 20)
	e.press("p")
	e.settle()
	if !strings.Contains(e.screen(), "Pull image") {
		t.Fatalf("no prompt:\n%s", e.screen())
	}
	e.typeText("redis:7")
	e.press("enter")
	e.waitStatus("pulling redis:7: 1/3 layers")

	// The UI stays responsive while the pull runs.
	e.selectImage("alpine:3.20")
	e.press("j")
	if r, _ := e.imagesView().selected(); r.label != "app:1.0" {
		t.Errorf("selection %q after j, want app:1.0", r.label)
	}

	close(gate)
	e.waitStatus("pull redis:7: done")
	e.until("pulled image listed", func() bool { return e.rowOf("redis:7") != "" })
	if c := pullCalls(f); len(c) != 1 || c[0].Args[0] != "redis:7" {
		t.Errorf("ImagePull calls %v", c)
	}
}

func TestPullDefaultsToLatest(t *testing.T) {
	f := imageSample()
	f.SetPullScript("redis", dockerfake.PullScript{Image: img(imgID('7'), 1000, day)})
	e := imagesEnv(t, f, 100, 20)
	e.press("p")
	e.settle()
	e.typeText("redis")
	e.press("enter")
	e.waitStatus("pull redis:latest: done")
	if c := pullCalls(f); len(c) != 1 || c[0].Args[0] != "redis:latest" {
		t.Errorf("ImagePull calls %v, want redis:latest", c)
	}
	e.until("pulled image listed", func() bool { return e.rowOf("redis:latest") != "" })
}

func TestPullFails(t *testing.T) {
	f := imageSample()
	f.SetPullScript("acme/app:1", dockerfake.PullScript{
		Messages: layerMsgs("l1"),
		Err:      errors.New("manifest for acme/app:1 not found: manifest unknown"),
	})
	e := imagesEnv(t, f, 100, 20)

	e.press("p")
	e.settle()
	e.typeText("acme/app:1")
	e.press("enter")
	e.waitStatus("pull acme/app:1: manifest for acme/app:1 not found: manifest unknown")

	// A repository the registry does not know fails before streaming.
	e.press("p")
	e.settle()
	e.typeText("nosuch/thing")
	e.press("enter")
	e.waitStatus("pull nosuch/thing:latest: pull access denied for nosuch/thing")
	if e.rowOf("acme/app:1") != "" {
		t.Error("failed pull listed an image")
	}
}

func TestPullInvalidReferenceNotSent(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 20)
	e.press("p")
	e.settle()
	e.typeText("Not/Valid:Ref!")
	e.press("enter")
	e.waitStatus("pull: invalid image reference")
	if c := pullCalls(f); len(c) != 0 {
		t.Errorf("ImagePull called with an invalid reference: %v", c)
	}
}

func TestPullPromptCancelled(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 20)
	e.press("p")
	e.settle()
	e.typeText("redis")
	e.press("esc")
	e.settle()
	if strings.Contains(e.screen(), "Pull image") {
		t.Errorf("prompt still open:\n%s", e.screen())
	}
	// An empty entry does nothing either.
	e.press("p")
	e.settle()
	e.press("enter")
	e.settle()
	if c := pullCalls(f); len(c) != 0 {
		t.Errorf("ImagePull called: %v", c)
	}
	if st := e.statusLine(); st != "" {
		t.Errorf("status %q, want none", st)
	}
}

func TestPullProgress(t *testing.T) {
	t.Parallel()
	var p pullProgress
	if p.text() != "" {
		t.Errorf("text before any layer %q", p.text())
	}
	for _, m := range []jsonstream.Message{
		{ID: "7", Status: "Pulling from library/redis"},
		{ID: "a", Status: "Pulling fs layer"},
		{ID: "b", Status: "Already exists"},
		{ID: "a", Status: "Downloading"},
		{ID: "a", Status: "Pull complete"},
		{ID: "a", Status: "Pull complete"}, // repeated: counted once
		{ID: "c", Status: "Waiting"},
		{Status: "Digest: sha256:..."},
	} {
		p.observe(m)
	}
	if got := p.text(); got != "2/3 layers" {
		t.Errorf("text %q, want 2/3 layers", got)
	}
	for i := range maxPullLayers + 10 {
		p.observe(jsonstream.Message{ID: string(rune(0x1000 + i)), Status: "Waiting"})
	}
	if len(p.layers) != maxPullLayers {
		t.Errorf("tracked %d layers, want the cap %d", len(p.layers), maxPullLayers)
	}
}

func TestPullImageCancelled(t *testing.T) {
	t.Parallel()
	f := dockerfake.New()
	f.SetPullScript("redis", dockerfake.PullScript{Messages: layerMsgs("l1")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pullImage(ctx, f, "redis:latest", func(string) {}); err == nil {
		t.Error("cancelled pull reported success")
	}
}

func pruneCalls(f *dockerfake.Service) []dockerfake.Call {
	return f.CallsTo(dockerfake.MethodImagePruneDangling)
}

func TestPruneDanglingImages(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 24)
	e.press("P")
	e.settle()
	s := e.screen()
	if !strings.Contains(s, "Prune dangling images?") || !strings.Contains(s, "2 dangling images (450MB) will be removed:") {
		t.Fatalf("confirmation:\n%s", s)
	}
	for _, want := range []string{"dddddddddddd  300MB", "eeeeeeeeeeee  150MB"} {
		if !strings.Contains(s, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, s)
		}
	}
	// The dangling image used by the stopped container builder is kept.
	if strings.Contains(s, "ffffffffffff") {
		t.Errorf("confirmation lists an image in use:\n%s", s)
	}
	e.press("y")
	e.waitStatus("prune dangling images: reclaimed 450MB")
	if c := pruneCalls(f); len(c) != 1 {
		t.Errorf("%d prune calls, want 1", len(c))
	}
	e.press("a")
	e.until("pruned images gone", func() bool { return strings.Count(e.screen(), "<none>:<none>") == 1 })
	if !strings.Contains(e.screen(), "ffffffffffff") {
		t.Errorf("in-use dangling image removed:\n%s", e.screen())
	}
}

func TestPruneNothingToPrune(t *testing.T) {
	f := dockerfake.New()
	f.AddImage(img(imgID('a'), 1000, day, "nginx:1.27"))
	f.AddImage(img(imgID('f'), 1000, day))
	f.AddContainer(user(0, "builder", imgID('f'), imgID('f'), container.StateExited))
	e := imagesEnv(t, f, 100, 20)
	e.press("P")
	e.settle()
	if strings.Contains(e.screen(), "Prune dangling images?") {
		t.Fatalf("confirmation shown with nothing to prune:\n%s", e.screen())
	}
	e.waitStatus("nothing to prune")
	if c := pruneCalls(f); len(c) != 0 {
		t.Errorf("prune called: %v", c)
	}
}

func TestPruneCancelled(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 24)
	e.press("P")
	e.settle()
	e.press("esc")
	e.settle()
	if c := pruneCalls(f); len(c) != 0 {
		t.Errorf("prune called after cancel: %v", c)
	}
}

func TestImageActionsRefusedWhileDisconnected(t *testing.T) {
	f := imageSample()
	e := imagesEnv(t, f, 100, 20)
	f.Disconnect(errors.New("connection refused"))
	e.until("disconnected", func() bool { return e.st.State() == store.StateDisconnected })
	f.ResetCalls()

	e.selectImage("alpine:3.20")
	e.press("d")
	e.settle()
	e.press("y")
	e.waitStatus("delete alpine:3.20: not connected")

	e.press("p")
	e.settle()
	e.typeText("redis")
	e.press("enter")
	e.waitStatus("pull redis:latest: not connected")
	if c := f.Calls(); slices.ContainsFunc(c, func(c dockerfake.Call) bool {
		return c.Method == dockerfake.MethodImageRemove || c.Method == dockerfake.MethodImagePull
	}) {
		t.Errorf("engine called while disconnected: %v", c)
	}
}
