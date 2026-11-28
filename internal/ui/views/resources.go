package views

import (
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
)

// Prefs holds display settings shared by every view of a session. Its zero
// value is the startup setting, and it is never saved, so every run starts
// from it. It is read and written only on the UI goroutine.
type Prefs struct {
	// Stats shows the live CPU and MEM columns instead of PORTS in every
	// containers view.
	Stats bool
	// Sort is the row order of every containers view. Invariant: a Sort
	// other than sortName implies Stats, since the CPU and MEM orders sort
	// on the stats columns. Choosing such an order turns Stats on, and
	// turning Stats off resets Sort to sortName.
	Sort sortOrder
}

// sortOrder is a containers-view row order. The zero value, sortName, is the
// startup order.
type sortOrder int

const (
	// sortName orders rows by name, then ID.
	sortName sortOrder = iota
	// sortCPU orders rows by the CPU value shown, largest first, with rows
	// showing no value last.
	sortCPU
	// sortMem orders rows by memory usage, largest first, with rows showing
	// no value last.
	sortMem
)

// Resources returns the root views the command bar and the resource tabs
// can switch to, in tab order, backed by st. The views share one Prefs, so
// a setting changed in one applies to all of them, drill-downs included,
// for the rest of the session.
func Resources(st *store.Store) []ui.Resource {
	prefs := &Prefs{}
	return []ui.Resource{
		{
			Name: "containers", Aliases: []string{"co"},
			New:   func(snap store.Snapshot) ui.View { return NewContainers(st, snap, prefs) },
			Count: store.Snapshot.NumContainers,
		},
		{
			Name: "images", Aliases: []string{"im"},
			New:   func(snap store.Snapshot) ui.View { return NewImages(st, snap, prefs) },
			Count: store.Snapshot.NumImages,
		},
		{
			Name: "volumes", Aliases: []string{"vol"},
			New:   func(snap store.Snapshot) ui.View { return NewVolumes(st, snap, prefs) },
			Count: store.Snapshot.NumVolumes,
		},
		{
			Name: "projects", Aliases: []string{"pr"},
			New:   func(snap store.Snapshot) ui.View { return NewProjects(st, snap, prefs) },
			Count: store.Snapshot.NumProjects,
		},
		{
			Name: "networks", Aliases: []string{"net"},
			New:   func(snap store.Snapshot) ui.View { return NewNetworks(st, snap, prefs) },
			Count: store.Snapshot.NumNetworks,
		},
	}
}

// orNewPrefs returns p, or new private settings when p is nil, so a view
// built without shared settings still works on its own.
func orNewPrefs(p *Prefs) *Prefs {
	if p == nil {
		return &Prefs{}
	}
	return p
}
