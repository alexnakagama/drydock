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
