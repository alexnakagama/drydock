package keys

import (
	"slices"
	"testing"

	"charm.land/bubbles/v2/key"
)

func TestSwitchHelpMatchesSwitchKeys(t *testing.T) {
	g := Global
	var matched []string
	for _, b := range append(g.TabKeys(), g.NextTab, g.PrevTab) {
		matched = append(matched, b.Keys()...)
	}
	if got := g.SwitchHelp.Keys(); !slices.Equal(got, matched) {
		t.Errorf("SwitchHelp keys %q, want the switching keys %q", got, matched)
	}
	if !g.SwitchHelp.Enabled() {
		t.Error("SwitchHelp is disabled, so the help overlay would dim it")
	}
	if !slices.ContainsFunc(g.Bindings(), func(b key.Binding) bool { return b.Help() == g.SwitchHelp.Help() }) {
		t.Error("SwitchHelp is not listed in Bindings")
	}
	for _, b := range append(g.TabKeys(), g.NextTab, g.PrevTab) {
		if b.Help().Key != "" {
			t.Errorf("switch binding %q has help; only SwitchHelp is listed", b.Keys())
		}
	}
}

// TestSwitchKeysReserved fails if a view keymap binds a key the root takes
// for switching resources, which would shadow the view's binding.
func TestSwitchKeysReserved(t *testing.T) {
	maps := map[string][]key.Binding{
		"table":      Table.Bindings(),
		"pager":      Pager.Bindings(),
		"containers": Containers.Bindings(),
		"images":     Images.Bindings(),
		"volumes":    Volumes.Bindings(),
		"projects":   Projects.Bindings(),
		"networks":   Networks.Bindings(),
		"logs":       {Logs.Follow, Logs.Search, Logs.Reopen},
		"global":     {Global.Command, Global.Help, Global.Back, Global.Quit, Global.ForceQuit},
	}
	for name, bs := range maps {
		for _, b := range bs {
			for _, k := range b.Keys() {
				if slices.Contains(switchKeys, k) {
					t.Errorf("%s binding %q uses the reserved switch key %q", name, b.Help().Key, k)
				}
			}
		}
	}
}

// TestContainersKeysDistinct fails if two bindings the containers view
// answers to share a key: its own actions, the list keys it shares with
// every list view, and the global keys the root takes before it.
func TestContainersKeysDistinct(t *testing.T) {
	bs := slices.Concat(Containers.Bindings(), Table.Bindings(),
		[]key.Binding{Global.Command, Global.Help, Global.Back, Global.Quit, Global.ForceQuit},
		Global.TabKeys(), []key.Binding{Global.NextTab, Global.PrevTab})
	owner := map[string]string{}
	for _, b := range bs {
		for _, k := range b.Keys() {
			if prev, ok := owner[k]; ok {
				t.Errorf("key %q is bound by both %q and %q", k, prev, b.Help().Desc)
				continue
			}
			owner[k] = b.Help().Desc
		}
	}
	for _, want := range []struct {
		name string
		b    key.Binding
		key  string
	}{
		{"Stats", Containers.Stats, "t"},
		{"Pause", Containers.Pause, "p"},
		{"Prune", Containers.Prune, "P"},
	} {
		if !slices.Equal(want.b.Keys(), []string{want.key}) || want.b.Help().Key != want.key {
			t.Errorf("%s binding = keys %q help %q, want %s", want.name, want.b.Keys(), want.b.Help().Key, want.key)
		}
	}
}

// TestProjectsKeysDistinct fails if two bindings the projects view answers
// to share a key, and checks the keys of its project actions, which match
// the single-container s and r.
func TestProjectsKeysDistinct(t *testing.T) {
	bs := slices.Concat(Projects.Bindings(), Table.Bindings(),
		[]key.Binding{Global.Command, Global.Help, Global.Back, Global.Quit, Global.ForceQuit},
		Global.TabKeys(), []key.Binding{Global.NextTab, Global.PrevTab})
	owner := map[string]string{}
	for _, b := range bs {
		for _, k := range b.Keys() {
			if prev, ok := owner[k]; ok {
				t.Errorf("key %q is bound by both %q and %q", k, prev, b.Help().Desc)
				continue
			}
			owner[k] = b.Help().Desc
		}
	}
	for name, want := range map[string]struct {
		b   key.Binding
		key string
	}{
		"StartStop": {Projects.StartStop, Containers.StartStop.Help().Key},
		"Restart":   {Projects.Restart, Containers.Restart.Help().Key},
	} {
		if !slices.Equal(want.b.Keys(), []string{want.key}) || want.b.Help().Key != want.key {
			t.Errorf("%s binding = keys %q help %q, want %s", name, want.b.Keys(), want.b.Help().Key, want.key)
		}
	}
}

// TestContainersBindingsListPauseAndPrune fails if p or P is bound but left
// out of Bindings, which would hide it from the footer and help.
func TestContainersBindingsListPauseAndPrune(t *testing.T) {
	var listed []string
	for _, b := range Containers.Bindings() {
		listed = append(listed, b.Keys()...)
	}
	for _, k := range []string{"p", "P"} {
		if !slices.Contains(listed, k) {
			t.Errorf("containers Bindings() does not list %q", k)
		}
	}
}

// TestFiveTabKeys fails if a number key is missing for one of the five
// resources, or if a tab key is not reserved from the views.
func TestFiveTabKeys(t *testing.T) {
	tabs := Global.TabKeys()
	want := []string{"1", "2", "3", "4", "5"}
	if len(tabs) != len(want) {
		t.Fatalf("TabKeys() has %d bindings, want %d", len(tabs), len(want))
	}
	for i, b := range tabs {
		if !slices.Equal(b.Keys(), []string{want[i]}) {
			t.Errorf("TabKeys()[%d] keys %q, want %q", i, b.Keys(), want[i])
		}
		if !slices.Contains(switchKeys, want[i]) {
			t.Errorf("tab key %q is not reserved in switchKeys", want[i])
		}
	}
	if got := Global.SwitchHelp.Help().Key; got != "1-5/tab" {
		t.Errorf("SwitchHelp help key %q, want %q", got, "1-5/tab")
	}
}

// TestNetworksKeysDistinct fails if two bindings the networks view answers
// to share a key, and checks that its actions use the keys the other list
// views use for the same actions.
func TestNetworksKeysDistinct(t *testing.T) {
	bs := slices.Concat(Networks.Bindings(), Table.Bindings(),
		[]key.Binding{Global.Command, Global.Help, Global.Back, Global.Quit, Global.ForceQuit},
		Global.TabKeys(), []key.Binding{Global.NextTab, Global.PrevTab})
	owner := map[string]string{}
	for _, b := range bs {
		for _, k := range b.Keys() {
			if prev, ok := owner[k]; ok {
				t.Errorf("key %q is bound by both %q and %q", k, prev, b.Help().Desc)
				continue
			}
			owner[k] = b.Help().Desc
		}
	}
	for name, want := range map[string]struct {
		b, like key.Binding
	}{
		"Containers": {Networks.Containers, Volumes.Containers},
		"Inspect":    {Networks.Inspect, Volumes.Inspect},
		"Delete":     {Networks.Delete, Volumes.Delete},
		"Prune":      {Networks.Prune, Volumes.Prune},
	} {
		if !slices.Equal(want.b.Keys(), want.like.Keys()) {
			t.Errorf("Networks.%s keys %q, want %q as in the volumes view", name, want.b.Keys(), want.like.Keys())
		}
	}
}
