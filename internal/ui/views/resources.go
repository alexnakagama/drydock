package views

import (
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
)

// Resources returns the root views the command bar can switch to, backed
// by st.
func Resources(st *store.Store) []ui.Resource {
	return []ui.Resource{
		{Name: "containers", Aliases: []string{"co"}, New: func(snap store.Snapshot) ui.View {
			return NewContainers(st, snap)
		}},
		{Name: "images", Aliases: []string{"im"}, New: func(snap store.Snapshot) ui.View {
			return NewImages(st, snap)
		}},
		{Name: "volumes", Aliases: []string{"vol"}, New: func(snap store.Snapshot) ui.View {
			return NewVolumes(st, snap)
		}},
	}
}
