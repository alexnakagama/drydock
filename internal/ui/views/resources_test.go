package views

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// Every resource needs a number key, and the tab strip a count.
func TestResourcesFitTabKeys(t *testing.T) {
	rs := Resources(nil)
	if n, max := len(rs), len(keys.Global.TabKeys()); n > max {
		t.Errorf("%d resources but only %d number keys", n, max)
	}
	for _, r := range rs {
		if r.Count == nil {
			t.Errorf("resource %s has no Count", r.Name)
		}
	}
}

// The resources are listed in tab order, with the names and aliases the
// command bar accepts.
func TestResourcesOrder(t *testing.T) {
	var got []string
	for _, r := range Resources(nil) {
		got = append(got, r.Name+"/"+strings.Join(r.Aliases, ","))
	}
	want := []string{"containers/co", "images/im", "volumes/vol", "projects/pr"}
	if !slices.Equal(got, want) {
		t.Errorf("resources %q, want %q", got, want)
	}
}

// A fresh Resources, as after a restart, starts with stats off, and its
// views share one Prefs, so the setting is session-wide.
func TestResourcesShareSessionPrefsStartingOff(t *testing.T) {
	if (Prefs{}).Stats {
		t.Fatal("the zero Prefs has stats on")
	}
	st := store.New(dockerfake.New(), func(tea.Msg) {})
	rs := Resources(st)
	cv := rs[0].New(store.Snapshot{}).(*containersView)
	iv := rs[1].New(store.Snapshot{}).(*imagesView)
	vv := rs[2].New(store.Snapshot{}).(*volumesView)
	pv := rs[3].New(store.Snapshot{}).(*projectsView)
	t.Cleanup(func() { cv.Close(); iv.Close(); vv.Close(); pv.Close() })
	if cv.prefs.Stats || cv.statsCols {
		t.Error("a fresh containers view has stats on")
	}
	if cv.prefs != iv.prefs || cv.prefs != vv.prefs || cv.prefs != pv.prefs {
		t.Error("the resource views do not share one Prefs")
	}
	if again := Resources(st)[0].New(store.Snapshot{}).(*containersView); again.prefs == cv.prefs {
		t.Error("a new Resources reuses the previous session's Prefs")
	}
}
