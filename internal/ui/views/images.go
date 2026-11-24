package views

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/docker/go-units"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// pullTimeout bounds an image pull. Pulls of large images over slow links
// legitimately take far longer than other actions.
const pullTimeout = 30 * time.Minute

// Placeholders the engine reports for an image without tags or digests.
const (
	noneTag    = "<none>:<none>"
	noneDigest = "<none>@<none>"
)

// imageColumns are the images view's columns. REPOSITORY:TAG is searched by
// the filter.
var imageColumns = []components.Column{
	{Title: "REPOSITORY:TAG", Width: 30, Flex: true, Filter: true},
	{Title: "ID", Width: 12},
	{Title: "SIZE", Width: 9},
	{Title: "CREATED", Width: 16},
	{Title: "IN USE", Width: 6},
}

// imagesConfig holds what the images view takes from its environment.
// Tests replace it.
type imagesConfig struct {
	// now is the reference time for the CREATED column.
	now func() time.Time
}

func defaultImagesConfig() imagesConfig { return imagesConfig{now: time.Now} }

// imageRow is one row of the images view: an image as seen through one of
// its references.
type imageRow struct {
	img image.Summary
	// ref is what actions on the row address: the tag; for an image with
	// digests but no tag, the image ID when all its digests belong to one
	// repository (removing it removes them all), otherwise a digest
	// reference of the row's repository; and the image ID for a dangling
	// image.
	ref string
	// label is the REPOSITORY:TAG cell, for example "nginx:1.27",
	// "nginx:<none>" or "<none>:<none>".
	label string
	// tag reports whether ref is a tag.
	tag bool
	// dangling reports whether the image has neither tags nor digests.
	dangling bool
}

// name returns how the row's image is named in breadcrumbs and messages:
// its label, or its short ID when it has no reference at all.
func (r imageRow) name() string {
	if r.dangling {
		return store.ShortID(r.img.ID)
	}
	return r.label
}

// key identifies the row across refreshes. Several rows share an image ID
// (one per tag), so the reference is part of the key.
func (r imageRow) key() string { return r.img.ID + "\x00" + r.ref }

// imagesView lists images, one row per tag, and acts on the selected one.
// Dangling images are hidden until `a` is pressed.
type imagesView struct {
	st   *store.Store
	snap store.Snapshot
	cfg  imagesConfig
	all  bool

	table components.Table
	// shown maps the keys of the listed rows to their data in snap.
	shown map[string]imageRow
}

// NewImages returns the images view, listing tagged images until `a` is
// pressed.
func NewImages(st *store.Store, snap store.Snapshot) ui.View {
	return newImagesView(st, snap, defaultImagesConfig())
}

func newImagesView(st *store.Store, snap store.Snapshot, cfg imagesConfig) *imagesView {
	v := &imagesView{
		st:    st,
		snap:  snap,
		cfg:   cfg,
		table: components.NewTable(imageColumns),
	}
	v.refresh()
	return v
}

func (v *imagesView) Init() tea.Cmd { return nil }

func (v *imagesView) SetSize(w, h int) { v.table.SetSize(w, h) }

func (v *imagesView) View() string { return v.table.View() }

func (v *imagesView) Crumbs() []string { return []string{"images"} }

func (v *imagesView) Indicators() []string {
	ind := []string{"tagged"}
	if v.all {
		ind[0] = "all"
	}
	if f := v.table.Filter(); f != "" {
		ind = append(ind, "/"+f)
	}
	return ind
}

func (v *imagesView) KeyHelp() []key.Binding {
	return append(keys.Images.Bindings(), keys.Table.Filter)
}

func (v *imagesView) InputFocused() bool { return v.table.InputFocused() }

func (v *imagesView) Back() bool { return v.table.Back() }

func (v *imagesView) Close() {}

func (v *imagesView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case store.SnapshotMsg:
		v.snap = msg.Snapshot
		v.refresh()
		return nil
	case tea.PasteMsg:
		_, cmd := v.table.Update(msg)
		return cmd
	case tea.KeyPressMsg:
		return v.handleKey(msg)
	default:
		// The filter input's own messages (cursor) while it is focused.
		if v.table.InputFocused() {
			_, cmd := v.table.Update(msg)
			return cmd
		}
		return nil
	}
}

func (v *imagesView) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if ok, cmd := v.table.Update(msg); ok {
		return cmd
	}
	km := keys.Images
	switch {
	case key.Matches(msg, km.ToggleAll):
		v.all = !v.all
		v.refresh()
		return nil
	case key.Matches(msg, km.Pull):
		return ui.Prompt("Pull image (anonymous)", "e.g. redis:7", v.pull)
	case key.Matches(msg, km.Prune):
		return v.confirmPrune()
	}
	r, ok := v.selected()
	if !ok {
		return nil
	}
	switch {
	case key.Matches(msg, km.Containers):
		return v.showContainers(r)
	case key.Matches(msg, km.Delete):
		return v.confirmDelete(r)
	case key.Matches(msg, km.Inspect):
		return v.inspect(r)
	}
	return nil
}

// selected returns the selected row as of the current snapshot.
func (v *imagesView) selected() (imageRow, bool) {
	r, ok := v.table.Selected()
	if !ok {
		return imageRow{}, false
	}
	row, ok := v.shown[r.Key]
	return row, ok
}

// refresh rebuilds the rows from the snapshot and the current mode.
func (v *imagesView) refresh() {
	var list []imageRow
	hidden := 0
	for _, img := range v.snap.Images() {
		for _, r := range imageRows(img) {
			if r.dangling && !v.all {
				hidden++
				continue
			}
			list = append(list, r)
		}
	}
	// Dangling images last; within each group by reference, then ID.
	slices.SortFunc(list, func(a, b imageRow) int {
		if a.dangling != b.dangling {
			if a.dangling {
				return 1
			}
			return -1
		}
		return cmp.Or(cmp.Compare(a.label, b.label), cmp.Compare(a.img.ID, b.img.ID), cmp.Compare(a.ref, b.ref))
	})

	now := v.cfg.now()
	v.shown = make(map[string]imageRow, len(list))
	rows := make([]components.Row, 0, len(list))
	for _, r := range list {
		k := r.key()
		if _, dup := v.shown[k]; dup {
			continue
		}
		v.shown[k] = r
		rows = append(rows, components.Row{Key: k, Cells: []string{
			r.label,
			store.ShortID(r.img.ID),
			humanSize(r.img.Size),
			humanAge(r.img.Created, now),
			fmt.Sprint(v.snap.ImageUseCount(r.img.ID)),
		}})
	}

	switch {
	case !v.snap.Loaded():
		v.table.SetEmptyText("Loading images…")
	case !v.all && hidden > 0:
		v.table.SetEmptyText(fmt.Sprintf("No tagged images (%d dangling; press a to show all).", hidden))
	default:
		v.table.SetEmptyText("No images.")
	}
	v.table.SetRows(rows)
}

// imageRows returns the rows listing img: one per tag; for an image with
// digests but no tag (pulled by digest), one per repository, shown as
// "repo:<none>"; and for an image with neither, one dangling row shown as
// "<none>:<none>".
func imageRows(img image.Summary) []imageRow {
	if tags := imageTags(img); len(tags) > 0 {
		rows := make([]imageRow, len(tags))
		for i, t := range tags {
			rows[i] = imageRow{img: img, ref: t, label: t, tag: true}
		}
		return rows
	}
	var rows []imageRow
	seen := make(map[string]bool)
	for _, d := range imageDigests(img) {
		repo, _, _ := strings.Cut(d, "@")
		if seen[repo] {
			continue
		}
		seen[repo] = true
		rows = append(rows, imageRow{img: img, ref: d, label: repo + ":<none>"})
	}
	switch len(rows) {
	case 0:
		return []imageRow{{img: img, ref: img.ID, label: noneTag, dangling: true}}
	case 1:
		// One repository: removing by ID removes every digest of it,
		// where removing one digest would leave the others (and the row).
		rows[0].ref = img.ID
	default:
		// Several repositories: removing by ID needs force, so each row
		// removes its repository's first digest. A repository with more
		// digests keeps its row, as the confirmation says.
	}
	return rows
}

// imageDigests returns img's digest references without duplicates and
// without the placeholder older engines report.
func imageDigests(img image.Summary) []string {
	digests := make([]string, 0, len(img.RepoDigests))
	for _, d := range img.RepoDigests {
		repo, _, ok := strings.Cut(d, "@")
		if d == noneDigest || !ok || repo == "" || slices.Contains(digests, d) {
			continue
		}
		digests = append(digests, d)
	}
	return digests
}

// remainingRefs returns the references that still point to the row's image
// after its ref is removed, following the engine's rules: removing a tag
// also removes the digests of its repository unless another tag of that
// repository remains, removing a digest removes only that digest, and
// removing by ID removes everything. A non-empty result means the engine
// only untags (which succeeds even if the image is in use); an empty one
// means it removes the image, or refuses to if a container uses it.
func remainingRefs(r imageRow) []string {
	if r.ref == r.img.ID {
		return nil
	}
	tags, digests := imageTags(r.img), imageDigests(r.img)
	repo := docker.RepositoryName(r.ref)
	sameRepo := func(ref string) bool { return repo != "" && docker.RepositoryName(ref) == repo }

	var rest []string
	keepRepoDigests := !r.tag
	for _, t := range tags {
		if t == r.ref {
			continue
		}
		rest = append(rest, t)
		if sameRepo(t) {
			keepRepoDigests = true
		}
	}
	for _, d := range digests {
		if d == r.ref || (!keepRepoDigests && sameRepo(d)) {
			continue
		}
		rest = append(rest, d)
	}
	return rest
}

// shortRef shortens the digest of a digest reference for display.
func shortRef(ref string) string {
	if repo, dg, ok := strings.Cut(ref, "@"); ok {
		return repo + "@" + store.ShortID(dg)
	}
	return ref
}

// imageTags returns img's tags without duplicates and without the
// placeholder older engines report for untagged images.
func imageTags(img image.Summary) []string {
	tags := make([]string, 0, len(img.RepoTags))
	for _, t := range img.RepoTags {
		if t != "" && t != noneTag && !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	return tags
}

// isDanglingImage reports whether img has neither tags nor digests, the
// images the engine's dangling prune removes when no container uses them.
func isDanglingImage(img image.Summary) bool {
	rows := imageRows(img)
	return len(rows) == 1 && rows[0].dangling
}

// humanSize formats a size in bytes as the docker CLI does ("187MB").
func humanSize(n int64) string {
	if n < 0 {
		return "N/A"
	}
	return units.HumanSizeWithPrecision(float64(n), 3)
}

// humanAge formats a creation time (Unix seconds) relative to now, as the
// docker CLI does ("2 weeks ago").
func humanAge(created int64, now time.Time) string {
	if created <= 0 {
		return "N/A"
	}
	d := now.Sub(time.Unix(created, 0))
	if d < 0 {
		d = 0 // clock skew between the engine and this machine
	}
	return units.HumanDuration(d) + " ago"
}

// confirmDelete asks before removing the selected reference. Removal is
// never forced: an image in use is refused by the engine, unless other
// references keep the image, in which case only the reference goes.
func (v *imagesView) confirmDelete(r imageRow) tea.Cmd {
	name := r.name()
	users := v.snap.ImageUsers(r.img.ID)
	rest := remainingRefs(r)
	var body []string
	switch {
	case len(rest) > 0:
		kind := "reference"
		if r.tag {
			kind = "tag"
		}
		kept := make([]string, len(rest))
		how := "tagged"
		for i, ref := range rest {
			kept[i] = shortRef(ref)
			if strings.Contains(ref, "@") {
				how = "referenced"
			}
		}
		body = append(body,
			fmt.Sprintf("Only the %s %s is removed.", kind, shortRef(r.ref)),
			fmt.Sprintf("The image stays %s as %s.", how, strings.Join(kept, ", ")))
	case len(users) > 0:
		body = append(body, fmt.Sprintf("%s is used by %s;", name, plural(len(users), "container", "containers")),
			"the engine will refuse to remove it:")
		body = append(body, containerLines(users)...)
	default:
		body = append(body, fmt.Sprintf("Image %s (%s) will be removed,", store.ShortID(r.img.ID), humanSize(r.img.Size)),
			"with any untagged parent images.")
	}
	ref, id, untagOnly := r.ref, r.img.ID, len(rest) > 0
	opts := docker.ImageRemoveOptions{Force: false, PruneChildren: true}
	return ui.Confirm("Delete image "+name+"?", body,
		ui.Track("deleting "+name, v.st.Action("delete "+name, func(ctx context.Context, svc docker.Service) error {
			if ref != id {
				// The dialog described the image the reference pointed to
				// in the snapshot. If it has moved since (a pull or a
				// retag), removing it now would act on another image.
				cur, err := svc.ImageInspect(ctx, ref)
				if err != nil {
					return err
				}
				if cur.Image.ID != id {
					return fmt.Errorf("%s now refers to a different image; nothing was removed, check and retry", shortRef(ref))
				}
				// Its other references may have changed too, turning an
				// untag into a removal or the reverse.
				now := r
				now.img.RepoTags, now.img.RepoDigests = cur.Image.RepoTags, cur.Image.RepoDigests
				if (len(remainingRefs(now)) > 0) != untagOnly {
					return fmt.Errorf("the references of %s changed; nothing was removed, check and retry", shortRef(ref))
				}
			}
			_, err := svc.ImageRemove(ctx, ref, opts)
			return err
		})))
}

// containerLines lists containers for a confirmation dialog, one per line.
func containerLines(cs []container.Summary) []string {
	lines := make([]string, len(cs))
	for i, c := range cs {
		lines[i] = "  " + store.ContainerName(c) + " (" + stateName(c.State) + ")"
	}
	return lines
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// inspect pushes the inspect view for the selected image. It inspects by
// ID, so a tag moved to another image meanwhile does not change what is
// shown.
func (v *imagesView) inspect(r imageRow) tea.Cmd {
	id := r.img.ID
	return ui.Push(newInspectView(v.st, r.name(), func(ctx context.Context, svc docker.Service) (json.RawMessage, error) {
		res, err := svc.ImageInspect(ctx, id)
		return res.Raw, err
	}))
}

// showContainers pushes a containers view listing the containers, running
// or stopped, created from the selected image.
func (v *imagesView) showContainers(r imageRow) tea.Cmd {
	id, name := r.img.ID, r.name()
	return ui.Push(NewFilteredContainers(v.st, v.snap, []string{name, "containers"},
		"No containers use "+name+".", func(c container.Summary) bool { return c.ImageID == id }))
}

// confirmPrune previews the dangling images the engine's prune would
// remove (those no container uses, running or stopped) and asks before
// pruning.
func (v *imagesView) confirmPrune() tea.Cmd {
	if !v.snap.Loaded() {
		return ui.Error("prune: images are still loading")
	}
	var cands []image.Summary
	var total int64
	for _, img := range v.snap.Images() {
		if isDanglingImage(img) && v.snap.ImageUseCount(img.ID) == 0 {
			cands = append(cands, img)
			total += max(img.Size, 0)
		}
	}
	if len(cands) == 0 {
		return ui.Success("nothing to prune: no unused dangling images")
	}
	slices.SortFunc(cands, func(a, b image.Summary) int {
		return cmp.Or(cmp.Compare(b.Created, a.Created), cmp.Compare(a.ID, b.ID))
	})
	now := v.cfg.now()
	body := []string{fmt.Sprintf("%s (%s) will be removed:",
		plural(len(cands), "dangling image", "dangling images"), humanSize(total))}
	for _, img := range cands {
		body = append(body, fmt.Sprintf("  %s  %s  %s", store.ShortID(img.ID), humanSize(img.Size), humanAge(img.Created, now)))
	}
	return ui.Confirm("Prune dangling images?", body,
		ui.Track("pruning dangling images", v.st.ActionWith("prune dangling images", 0,
			func(ctx context.Context, svc docker.Service) (string, error) {
				rep, err := svc.ImagePruneDangling(ctx)
				if err != nil {
					return "", err
				}
				return "reclaimed " + units.HumanSizeWithPrecision(float64(rep.SpaceReclaimed), 3), nil
			})))
}

// pull starts an anonymous pull of the reference typed into the prompt. An
// empty entry does nothing; an invalid one is reported without calling the
// engine.
func (v *imagesView) pull(input string) tea.Cmd {
	if input == "" {
		return nil
	}
	if !v.snap.Loaded() {
		return ui.Error("pull: images are still loading")
	}
	if v.isLocalImageID(input) {
		// A short ID is also a valid repository name; do not send a local
		// image ID to a registry.
		return ui.Error("pull: that is a local image ID; pull takes a reference such as redis:7")
	}
	ref, err := docker.NormalizePullRef(input)
	if err != nil {
		return ui.Error("pull: " + err.Error())
	}
	progress := make(chan string, 1)
	act := v.st.ActionWith("pull "+ref, pullTimeout, func(ctx context.Context, svc docker.Service) (string, error) {
		return pullImage(ctx, svc, ref, func(text string) { ui.SendLatest(progress, "pulling "+ref+": "+text) })
	})
	// The progress channel is closed however the action ends, including
	// when it is refused before reaching the engine.
	return ui.TrackProgress("pulling "+ref, progress, func() tea.Msg {
		defer close(progress)
		return act()
	})
}

// isLocalImageID reports whether s, with or without "sha256:", is a prefix
// of at least 12 hex digits of a known image's ID.
func (v *imagesView) isLocalImageID(s string) bool {
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) < 12 || strings.Trim(s, "0123456789abcdef") != "" {
		return false
	}
	return slices.ContainsFunc(v.snap.Images(), func(img image.Summary) bool {
		return strings.HasPrefix(strings.TrimPrefix(img.ID, "sha256:"), s)
	})
}

// pullImage pulls ref, calling report with a short progress text whenever
// it changes, and returns a success detail for the status line. A failure
// the registry reports in the stream is returned as an error carrying the
// registry's message.
func pullImage(ctx context.Context, svc docker.Service, ref string, report func(string)) (string, error) {
	stream, err := svc.ImagePull(ctx, ref)
	if err != nil {
		return "", err
	}
	// The outcome is decided by the messages read; a failure to release
	// the stream afterwards does not change it.
	defer func() { _ = stream.Close() }()

	var (
		prog     pullProgress
		last     string
		upToDate bool
	)
	for m, err := range stream.Messages(ctx) {
		if err != nil {
			return "", err
		}
		if m.Error != nil {
			msg := m.Error.Message
			if msg == "" {
				msg = "pull failed"
			}
			return "", errors.New(msg)
		}
		if strings.HasPrefix(m.Status, "Status: Image is up to date") {
			upToDate = true
		}
		prog.observe(m)
		if t := prog.text(); t != "" && t != last {
			last = t
			report(t)
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if upToDate {
		return "image is up to date", nil
	}
	return "", nil
}

// maxPullLayers bounds the layers a pull's progress tracks, so a
// misbehaving stream cannot grow it without limit.
const maxPullLayers = 1024

// pullProgress counts the layers of a pull and how many are done, from the
// engine's progress messages.
type pullProgress struct {
	layers map[string]bool // layer ID -> done
	done   int
}

func (p *pullProgress) observe(m jsonstream.Message) {
	if m.ID == "" {
		return
	}
	var done bool
	switch {
	case m.Status == "Pull complete" || m.Status == "Already exists":
		done = true
	case m.Status == "Pulling fs layer" || m.Status == "Waiting" || m.Status == "Downloading" ||
		m.Status == "Verifying Checksum" || m.Status == "Download complete" || m.Status == "Extracting" ||
		strings.HasPrefix(m.Status, "Retrying"):
	default:
		return // not a layer message (for example "Pulling from library/redis")
	}
	if p.layers == nil {
		p.layers = make(map[string]bool)
	}
	was, known := p.layers[m.ID]
	if !known && len(p.layers) >= maxPullLayers {
		return
	}
	if done && !was {
		p.done++
	}
	p.layers[m.ID] = was || done
}

// text describes the progress, or returns "" before any layer is known.
func (p *pullProgress) text() string {
	if len(p.layers) == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d layers", p.done, len(p.layers))
}
