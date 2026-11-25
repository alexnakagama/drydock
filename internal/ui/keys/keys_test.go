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
	if b := Containers.Stats; !slices.Equal(b.Keys(), []string{"t"}) || b.Help().Key != "t" {
		t.Errorf("Stats binding = keys %q help %q, want t", b.Keys(), b.Help().Key)
	}
}
