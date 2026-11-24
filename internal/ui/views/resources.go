package views

import (
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
)

// Resources returns the root views the command bar and the resource tabs
// can switch to, in tab order, backed by st.
func Resources(st *store.Store) []ui.Resource {
	return []ui.Resource{
		{
			Name: "containers", Aliases: []string{"co"},
			New:   func(snap store.Snapshot) ui.View { return NewContainers(st, snap) },
			Count: store.Snapshot.NumContainers,
		},
		{
			Name: "images", Aliases: []string{"im"},
			New:   func(snap store.Snapshot) ui.View { return NewImages(st, snap) },
			Count: store.Snapshot.NumImages,
		},
		{
			Name: "volumes", Aliases: []string{"vol"},
			New:   func(snap store.Snapshot) ui.View { return NewVolumes(st, snap) },
			Count: store.Snapshot.NumVolumes,
		},
	}
}
