package views

import (
	"context"
	"encoding/json"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/ui/components"
)

// rawInspect returns a fetch that answers with doc.
func rawInspect(doc string) inspectFetch {
	return func(context.Context, docker.Service) (json.RawMessage, error) {
		return json.RawMessage(doc), nil
	}
}

// pushInspect pushes an inspect view of doc built like the images, volumes
// and networks call sites (no options) and waits until want is shown.
func (e *env) pushInspect(name, doc, want string, opts ...inspectOption) {
	e.t.Helper()
	e.push(newInspectView(e.st, name, rawInspect(doc), opts...))
	e.until("inspection shown", func() bool { return strings.Contains(e.screen(), want) })
}

// distinctStateStyles makes the state styles render differently from every
// JSON style until the test ends (bold, since lipgloss renders an
// underlined style one character at a time). RowGood and JSONString share the success
// color, so without this a test could not tell a state value from a
// string. components.Styles is a package global: a test that calls this
// must not run in parallel.
func distinctStateStyles(t *testing.T) {
	t.Helper()
	saved := components.Styles
	t.Cleanup(func() { components.Styles = saved })
	components.Styles.RowGood = components.Styles.RowGood.Bold(true)
	components.Styles.RowWarn = components.Styles.RowWarn.Bold(true)
	components.Styles.RowDim = components.Styles.RowDim.Bold(true)
}

func TestInspectColorsTokens(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 80, 14)
	e.pushInspect("doc", `{"Image":"nginx:1.27","RestartCount":0,"Running":true,"Args":null,"Labels":{}}`, `"Image"`)
	raw := e.m.View().Content
	s := components.Styles
	for name, want := range map[string]string{
		"key and value colors differ": s.JSONKey.Render(`"Image"`) + s.JSONPunct.Render(":") + " " + s.JSONString.Render(`"nginx:1.27"`) + s.JSONPunct.Render(","),
		"number":                      s.JSONNumber.Render("0") + s.JSONPunct.Render(","),
		"true":                        s.JSONNumber.Render("true") + s.JSONPunct.Render(","),
		"null":                        s.JSONNumber.Render("null") + s.JSONPunct.Render(","),
		"empty block":                 s.JSONPunct.Render("{}"),
		"root":                        "\n" + s.JSONPunct.Render("{") + " ", // alone on its line
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("%s: %q not in\n%q", name, want, raw)
		}
	}
}

func TestInspectContainerStateColor(t *testing.T) {
	tests := []struct {
		name  string
		row   string
		value string
		style func() string
	}{
		{"running", "web", `"running"`, func() string { return components.Styles.RowGood.Render(`"running"`) }},
		{"exited", "old", `"exited"`, func() string { return components.Styles.RowDim.Render(`"exited"`) }},
		{"paused", "frozen", `"paused"`, func() string { return components.Styles.RowWarn.Render(`"paused"`) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			distinctStateStyles(t)
			f, _ := sample()
			e := newEnv(t, f, 80, 14)
			e.press("a") // all containers, so exited ones are listed
			e.selectRow(tt.row)
			e.press("i")
			e.until("inspection shown", func() bool { return strings.Contains(e.screen(), `"Status": `+tt.value) })
			if raw := e.m.View().Content; !strings.Contains(raw, tt.style()) {
				t.Errorf("State.Status not in the row's style %q:\n%q", tt.style(), raw)
			}
		})
	}
}

func TestInspectOtherStatusKeysUseStringColor(t *testing.T) {
	distinctStateStyles(t)
	f, _ := sample()
	h := ctr(cid(5), "checked", "nginx:1.27", container.StateRunning, "Up 1 minute (healthy)")
	h.Health = &container.HealthSummary{Status: container.Healthy}
	f.AddContainer(h)
	e := newEnv(t, f, 100, 40)
	e.selectRow("checked")
	e.press("i")
	e.until("health shown", func() bool { return strings.Contains(e.screen(), `"Status": "healthy"`) })
	raw := e.m.View().Content
	s := components.Styles
	if want := s.JSONString.Render(`"healthy"`); !strings.Contains(raw, want) {
		t.Errorf("State.Health.Status not in the string style %q:\n%q", want, raw)
	}
	if want := s.RowGood.Render(`"running"`); !strings.Contains(raw, want) {
		t.Errorf("State.Status not in the running style %q:\n%q", want, raw)
	}
}

// TestInspectStateNeedsContainer checks that an inspection opened without
// withContainerState, as for an image, colors State.Status as a string.
func TestInspectStateNeedsContainer(t *testing.T) {
	distinctStateStyles(t)
	f, _ := sample()
	e := newEnv(t, f, 80, 14)
	e.pushInspect("nginx:1.27", `{"State":{"Status":"running"}}`, `"running"`)
	raw := e.m.View().Content
	s := components.Styles
	if want := s.JSONString.Render(`"running"`); !strings.Contains(raw, want) {
		t.Errorf("image State.Status not in the string style %q:\n%q", want, raw)
	}
	if bad := s.RowGood.Render(`"running"`); strings.Contains(raw, bad) {
		t.Errorf("image State.Status in the running style:\n%q", raw)
	}
}

// TestInspectHorizontalScrollKeepsColor scrolls so the window starts in
// the middle of a string: the visible part keeps the string style, and the
// text is the same as without coloring.
func TestInspectHorizontalScrollKeepsColor(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 60, 14)
	long := strings.Repeat("abcdefgh", 10)
	e.pushInspect("doc", `{"Key":"`+long+`"}`, `"Key"`)
	e.press("right") // 8 cells: the window starts inside the key's line
	e.press("right", "right")
	raw := e.m.View().Content
	// `  "Key": "` is 10 cells; at 24 cells in, the window starts 14 bytes
	// into the string body.
	visible := long[14 : 14+60]
	if want := components.Styles.JSONString.Render(visible); !strings.Contains(raw, want) {
		t.Errorf("scrolled string not in the string style %q:\n%q", want, raw)
	}
	if !strings.Contains(e.screen(), visible) {
		t.Errorf("scrolled text %q not shown:\n%s", visible, e.screen())
	}
}

// TestInspectRecolorsOnPaletteChange checks that an open inspect view
// takes the light palette when the terminal reports a light background.
func TestInspectRecolorsOnPaletteChange(t *testing.T) {
	saved := components.Styles // a package global: this test must stay serial
	t.Cleanup(func() { components.Styles = saved })

	dark, light := components.NewStyles(true), components.NewStyles(false)
	f, _ := sample()
	e := newEnv(t, f, 80, 14)
	e.selectRow("web")
	e.press("i")
	e.until("inspection shown", func() bool { return strings.Contains(e.screen(), `"Status": "running"`) })
	if raw := e.m.View().Content; !strings.Contains(raw, dark.JSONKey.Render(`"Status"`)) {
		t.Fatalf("before the report, key not in the dark style:\n%q", raw)
	}
	e.deliver(tea.BackgroundColorMsg{Color: color.White})
	raw := e.m.View().Content
	if !strings.Contains(raw, light.JSONKey.Render(`"Status"`)) {
		t.Errorf("after a light report, key not in the light style:\n%q", raw)
	}
	if strings.Contains(raw, dark.JSONKey.Render(`"Status"`)) {
		t.Errorf("after a light report, key still in the dark style:\n%q", raw)
	}
}
