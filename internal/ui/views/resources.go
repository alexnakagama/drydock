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
}

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
