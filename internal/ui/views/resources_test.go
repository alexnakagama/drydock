package views

import (
	"testing"

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
