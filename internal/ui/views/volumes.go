package views

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// volumePruneTimeout bounds a volume prune, which removes the previewed
// volumes one by one.
const volumePruneTimeout = 5 * time.Minute

// volumeColumns are the volumes view's columns. NAME is searched by the
// filter.
var volumeColumns = []components.Column{
	{Title: "NAME", Width: 30, Flex: true, Filter: true},
	{Title: "DRIVER", Width: 10},
	{Title: "MOUNTPOINT", Width: 40, Flex: true},
	{Title: "IN USE", Width: 6},
}

var _ ui.Positioner = (*volumesView)(nil)

// volumesView lists volumes and acts on the selected one.
type volumesView struct {
	st    *store.Store
	snap  store.Snapshot
	prefs *Prefs // shared with the containers views it opens

	table components.Table
	// shown maps the names of the listed volumes to their data in snap.
	shown map[string]volume.Volume
}

// NewVolumes returns the volumes view. prefs are the session's settings,
// passed on to the containers views it opens; nil gives the view settings of
// its own.
func NewVolumes(st *store.Store, snap store.Snapshot, prefs *Prefs) ui.View {
	return newVolumesView(st, snap, prefs)
}

func newVolumesView(st *store.Store, snap store.Snapshot, prefs *Prefs) *volumesView {
	v := &volumesView{
		st:    st,
		snap:  snap,
		prefs: orNewPrefs(prefs),
		table: components.NewTable(volumeColumns),
	}
	v.refresh()
	return v
}

func (v *volumesView) Init() tea.Cmd { return nil }

func (v *volumesView) SetSize(w, h int) { v.table.SetSize(w, h) }

func (v *volumesView) View() string { return v.table.View() }

func (v *volumesView) Crumbs() []string { return []string{"volumes"} }

func (v *volumesView) Indicators() []string {
	if f := v.table.Filter(); f != "" {
		return []string{"/" + f}
	}
	return nil
}

// KeyHelp returns the view's bindings in priority order, as copies: the
// row actions are disabled when no row is selected, and `enter` when no
// container uses the selected volume. Key presses are still matched against
// keys.Volumes, so a disabled hint never disables its key.
func (v *volumesView) KeyHelp() []key.Binding {
	km := keys.Volumes
	ctrs, inspect, del := km.Containers, km.Inspect, km.Delete
	vol, ok := v.selected()
	switch {
	case !ok:
		disable(&ctrs, &inspect, &del)
	case v.snap.VolumeUseCount(vol.Name) == 0:
		disable(&ctrs)
	}
	return []key.Binding{ctrs, inspect, km.Create, del, keys.Table.Filter, km.Prune}
}

// Position reports the table's selection and row counts (ui.Positioner).
func (v *volumesView) Position() (selected, listed, total int, filtered bool) {
	return v.table.Position()
}

func (v *volumesView) InputFocused() bool { return v.table.InputFocused() }

func (v *volumesView) Back() bool { return v.table.Back() }

func (v *volumesView) Close() {}

func (v *volumesView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case store.SnapshotMsg:
		v.snap = msg.Snapshot
		v.refresh()
		return nil
	case tea.PasteMsg:
		_, cmd := v.table.Update(msg)
		return cmd
	case tea.BackgroundColorMsg:
		// The palette changed: the filter input restyles itself.
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

func (v *volumesView) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if ok, cmd := v.table.Update(msg); ok {
		return cmd
	}
	km := keys.Volumes
	switch {
	case key.Matches(msg, km.Create):
		return ui.Prompt("Create volume (local driver)", "name; leave empty for a generated name", v.create)
	case key.Matches(msg, km.Prune):
		return v.confirmPrune()
	}
	vol, ok := v.selected()
	if !ok {
		return nil
	}
	switch {
	case key.Matches(msg, km.Containers):
		return v.showContainers(vol.Name)
	case key.Matches(msg, km.Delete):
		return v.confirmDelete(vol)
	case key.Matches(msg, km.Inspect):
		return v.inspect(vol.Name)
	}
	return nil
}

// selected returns the selected volume as of the current snapshot.
func (v *volumesView) selected() (volume.Volume, bool) {
	r, ok := v.table.Selected()
	if !ok {
		return volume.Volume{}, false
	}
	vol, ok := v.shown[r.Key]
	return vol, ok
}

// refresh rebuilds the rows from the snapshot. Rows are keyed by volume
// name, which the engine keeps unique.
func (v *volumesView) refresh() {
	vols := v.snap.Volumes()
	slices.SortFunc(vols, func(a, b volume.Volume) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Driver, b.Driver))
	})
	v.shown = make(map[string]volume.Volume, len(vols))
	rows := make([]components.Row, 0, len(vols))
	for _, vol := range vols {
		if _, dup := v.shown[vol.Name]; dup || vol.Name == "" {
			continue
		}
		v.shown[vol.Name] = vol
		users := v.snap.VolumeUseCount(vol.Name)
		tone := components.ToneDefault
		if users == 0 {
			tone = components.ToneDim
		}
		rows = append(rows, components.Row{Key: vol.Name, Tone: tone, Cells: []string{
			vol.Name,
			vol.Driver,
			vol.Mountpoint,
			fmt.Sprint(users),
		}})
	}
	if v.snap.Loaded() {
		v.table.SetEmptyText("No volumes.")
	} else {
		v.table.SetEmptyText("Loading volumes…")
	}
	v.table.SetRows(rows)
}

// create creates a volume named name with the local driver, or with an
// engine-generated name if name is empty.
//
// The engine treats creating an existing name as a no-op that returns the
// existing volume, which would report success without creating anything.
// So an existing name is refused: first against the snapshot, then, since
// the snapshot may lag, by inspecting the name on the engine right before
// creating it. A volume another client creates in the short window between
// that inspection and the create is still reported as created; it is left
// unchanged either way.
func (v *volumesView) create(name string) tea.Cmd {
	if name == "" {
		return ui.Track("creating volume", v.st.ActionWith("create volume", 0,
			func(ctx context.Context, svc docker.Service) (string, error) {
				vol, err := svc.VolumeCreate(ctx, "")
				if err != nil {
					return "", err
				}
				return "created " + vol.Name, nil
			}))
	}
	if err := docker.ValidateVolumeName(name); err != nil {
		return ui.Error("create volume: " + err.Error())
	}
	if _, ok := v.shown[name]; ok {
		return ui.Error(volumeExists(name).Error())
	}
	label := "create volume " + name
	return ui.Track("creating volume "+name, v.st.ActionWith(label, 0,
		func(ctx context.Context, svc docker.Service) (string, error) {
			_, err := svc.VolumeInspect(ctx, name)
			switch {
			case err == nil:
				return "", volumeExists(name)
			case !cerrdefs.IsNotFound(err):
				return "", err
			}
			if _, err := svc.VolumeCreate(ctx, name); err != nil {
				return "", err
			}
			return "created", nil
		}))
}

func volumeExists(name string) error {
	return fmt.Errorf("volume %s already exists", name)
}

// unsupportedName returns an error if drydock cannot address the volume in
// an engine request (see docker.ValidateVolumeName). Such volumes, which
// only plugin drivers can create, are listed but cannot be acted on.
func unsupportedName(name string) error {
	if err := docker.ValidateVolumeName(name); err != nil {
		return fmt.Errorf("drydock cannot address volume %q: its name is not supported", name)
	}
	return nil
}

// confirmDelete asks before removing the volume. Removal is never forced:
// the engine refuses to remove a volume that a container, running or
// stopped, mounts.
func (v *volumesView) confirmDelete(vol volume.Volume) tea.Cmd {
	name := vol.Name
	if err := unsupportedName(name); err != nil {
		return ui.Error("delete: " + err.Error())
	}
	var body []string
	if users := v.snap.VolumeUsers(name); len(users) > 0 {
		body = append(body, fmt.Sprintf("%s is mounted by %s;", name, plural(len(users), "container", "containers")),
			"the engine will refuse to remove it:")
		body = append(body, containerLines(users)...)
	} else {
		body = append(body, fmt.Sprintf("Volume %s (%s driver) and all its data will be removed.", name, vol.Driver))
	}
	return ui.Confirm("Delete volume "+name+"?", body,
		ui.Track("deleting volume "+name, v.st.ActionWith("delete volume "+name, 0,
			func(ctx context.Context, svc docker.Service) (string, error) {
				switch err := removeVolume(ctx, svc, vol); {
				case errors.Is(err, errVolumeGone):
					return "already removed", nil
				case err != nil:
					return "", err
				}
				return "", nil
			})))
}

// errVolumeGone reports that a volume no longer exists.
var errVolumeGone = errors.New("volume no longer exists")

// removeVolume removes the volume described by want, a volume from the
// snapshot that the user confirmed, without force.
//
// Volumes are addressed by name, and a name can be reused: if the volume
// was removed and another created under its name since the snapshot,
// removing by name would destroy a volume the user never saw. So the volume
// is inspected first, and is not removed if its driver, creation time or
// labels differ from the snapshot's. It returns errVolumeGone (wrapped) if
// the volume does not exist any more.
//
// The check is best-effort: creation times have one-second precision (and
// some plugin drivers report none), so a replacement with the same driver
// and labels made within the same second goes unnoticed, and a volume can
// still be replaced between the inspection and the removal. Removal is
// never forced, so a replacement that is in use is refused by the engine.
func removeVolume(ctx context.Context, svc docker.Service, want volume.Volume) error {
	cur, err := svc.VolumeInspect(ctx, want.Name)
	if cerrdefs.IsNotFound(err) {
		return fmt.Errorf("%w: %w", errVolumeGone, err)
	}
	if err != nil {
		return err
	}
	if cur.Volume.Driver != want.Driver || cur.Volume.CreatedAt != want.CreatedAt ||
		!maps.Equal(cur.Volume.Labels, want.Labels) {
		return fmt.Errorf("volume %s was replaced since it was listed; nothing was removed, check and retry", want.Name)
	}
	err = svc.VolumeRemove(ctx, want.Name, docker.VolumeRemoveOptions{Force: false})
	if cerrdefs.IsNotFound(err) {
		return fmt.Errorf("%w: %w", errVolumeGone, err)
	}
	return err
}

// inspect pushes the inspect view for the named volume.
func (v *volumesView) inspect(name string) tea.Cmd {
	if err := unsupportedName(name); err != nil {
		return ui.Error("inspect: " + err.Error())
	}
	return ui.Push(newInspectView(v.st, name, func(ctx context.Context, svc docker.Service) (json.RawMessage, error) {
		res, err := svc.VolumeInspect(ctx, name)
		return res.Raw, err
	}))
}

// showContainers pushes a containers view listing the containers, running
// or stopped, that mount the named volume.
func (v *volumesView) showContainers(name string) tea.Cmd {
	return ui.Push(NewFilteredContainers(v.st, v.snap, v.prefs, []string{name, "containers"},
		"No containers mount "+name+".", func(c container.Summary) bool { return mountsVolume(c, name) }))
}

// mountsVolume reports whether c mounts the named volume, following the
// same rule as the snapshot's usage index.
func mountsVolume(c container.Summary, name string) bool {
	return slices.ContainsFunc(c.Mounts, func(m container.MountPoint) bool {
		return m.Type == mount.TypeVolume && m.Name == name
	})
}

// confirmPrune previews the volumes no container mounts, running or
// stopped, named and anonymous alike, and asks before removing them.
//
// The engine's own prune removes only anonymous volumes unless asked for
// all, and judges usage at the time it runs, so what it removes could
// differ from the preview. Instead exactly the previewed volumes are
// removed, one by one and without force (see removeVolume): one that became
// in use meanwhile is refused by the engine and reported.
func (v *volumesView) confirmPrune() tea.Cmd {
	if !v.snap.Loaded() {
		return ui.Error("prune: volumes are still loading")
	}
	var cands []volume.Volume
	unsupported := 0
	for name, vol := range v.shown {
		if v.snap.VolumeUseCount(name) > 0 {
			continue
		}
		if unsupportedName(name) != nil {
			unsupported++
			continue
		}
		cands = append(cands, vol)
	}
	if len(cands) == 0 {
		if unsupported > 0 {
			return ui.Success(fmt.Sprintf("nothing to prune: %s skipped (unsupported names)",
				plural(unsupported, "unused volume", "unused volumes")))
		}
		return ui.Success("nothing to prune: every volume is in use")
	}
	slices.SortFunc(cands, func(a, b volume.Volume) int { return cmp.Compare(a.Name, b.Name) })
	what := plural(len(cands), "unused volume", "unused volumes") + " and all their data"
	if len(cands) == 1 {
		what = "1 unused volume and all its data"
	}
	body := []string{what + " will be removed:"}
	for _, vol := range cands {
		body = append(body, "  "+vol.Name)
	}
	if unsupported > 0 {
		body = append(body, fmt.Sprintf("(%s with unsupported names skipped)", plural(unsupported, "volume", "volumes")))
	}
	return ui.Confirm("Prune unused volumes?", body,
		ui.Track("pruning unused volumes", v.st.ActionWith("prune unused volumes", volumePruneTimeout,
			func(ctx context.Context, svc docker.Service) (string, error) {
				return removeVolumes(ctx, svc, cands)
			})))
}

// maxListedFailures bounds the failed volumes a prune report names.
const maxListedFailures = 3

// removeVolumes removes the given volumes one by one (see removeVolume),
// carrying on past failures, and returns a summary such as "2 volumes
// removed, 1 already gone". If any removal failed, or ctx ended before all
// were done, it returns an error summarizing the outcome instead, naming the
// failed volumes before the first error, for example "2 removed, 1 failed
// (pgdata): pgdata: volume is in use". A volume whose removal ctx cut off
// is reported as interrupted: the engine may or may not have removed it.
func removeVolumes(ctx context.Context, svc docker.Service, vols []volume.Volume) (string, error) {
	var (
		removed, gone, notAttempted int
		failed                      []string
		interrupted                 string
		firstErr                    error
		stopped                     bool
	)
	for i, vol := range vols {
		if ctx.Err() != nil {
			stopped, notAttempted = true, len(vols)-i
			break
		}
		err := removeVolume(ctx, svc, vol)
		switch {
		case err == nil:
			removed++
		case errors.Is(err, errVolumeGone):
			gone++
		case ctx.Err() != nil && errors.Is(err, ctx.Err()):
			stopped, interrupted, notAttempted = true, vol.Name, len(vols)-i-1
		default:
			failed = append(failed, vol.Name)
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", vol.Name, err)
			}
		}
		if stopped {
			break
		}
	}

	parts := []string{fmt.Sprintf("%d removed", removed)}
	if gone > 0 {
		parts = append(parts, fmt.Sprintf("%d already gone", gone))
	}
	if len(failed) > 0 {
		listed := failed
		if len(listed) > maxListedFailures {
			listed = append(slices.Clip(listed[:maxListedFailures]), fmt.Sprintf("%d more", len(failed)-maxListedFailures))
		}
		parts = append(parts, fmt.Sprintf("%d failed (%s)", len(failed), strings.Join(listed, ", ")))
	}
	if interrupted != "" {
		parts = append(parts, "1 interrupted ("+interrupted+")")
	}
	if notAttempted > 0 {
		parts = append(parts, fmt.Sprintf("%d not attempted", notAttempted))
	}
	summary := strings.Join(parts, ", ")

	switch {
	case stopped && firstErr != nil:
		return "", fmt.Errorf("%s: %w; %w", summary, context.Cause(ctx), firstErr)
	case stopped:
		return "", fmt.Errorf("%s: %w", summary, context.Cause(ctx))
	case firstErr != nil:
		return "", fmt.Errorf("%s: %w", summary, firstErr)
	}
	if gone > 0 {
		return fmt.Sprintf("%s removed, %d already gone", plural(removed, "volume", "volumes"), gone), nil
	}
	return plural(removed, "volume", "volumes") + " removed", nil
}
