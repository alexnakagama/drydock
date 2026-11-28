package views

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// inspectTimeout bounds the inspect call.
const inspectTimeout = 10 * time.Second

// inspectFetch returns an object's raw inspection JSON.
type inspectFetch func(ctx context.Context, svc docker.Service) (json.RawMessage, error)

// inspectLoadedMsg delivers the outcome of an inspect view's fetch.
type inspectLoadedMsg struct {
	view   uint64
	lines  []string            // pretty-printed, display-safe lines
	spans  [][]components.Span // spans[i] styles lines[i]
	err    error
	reason string // err's display text
}

// inspectView shows an object's inspection data as pretty-printed JSON in
// a scrollable window, colored by token type. It is shared by containers,
// images, volumes and networks.
type inspectView struct {
	id     uint64
	st     *store.Store
	name   string
	fetch  inspectFetch
	ctx    context.Context
	cancel context.CancelFunc
	// containerState colors State.Status like the container's row; see
	// withContainerState.
	containerState bool

	loading bool
	errText string
	lines   []string
	spans   [][]components.Span // spans[i] styles lines[i]
	pager   components.Pager
	width   int
	height  int
}

// newInspectView returns an inspect view for the object called name (its
// breadcrumb reads "... > name > inspect"). The fetch runs when the view is
// pushed and is cancelled if the view is closed first.
func newInspectView(st *store.Store, name string, fetch inspectFetch, opts ...inspectOption) *inspectView {
	ctx, cancel := context.WithCancel(context.Background())
	v := &inspectView{
		id:      nextViewID(),
		st:      st,
		name:    name,
		fetch:   fetch,
		ctx:     ctx,
		cancel:  cancel,
		loading: true,
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// inspectOption configures an inspect view.
type inspectOption func(*inspectView)

// withContainerState marks the inspected object as a container: the value
// of its top-level State.Status is colored like the container's row in the
// list (see containerTone). Other kinds of object leave it out, so a
// State.Status key in, say, an image's data keeps the string color.
func withContainerState() inspectOption {
	return func(v *inspectView) { v.containerState = true }
}

func (v *inspectView) Init() tea.Cmd {
	view, st, fetch, parent, containerState := v.id, v.st, v.fetch, v.ctx, v.containerState
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, inspectTimeout)
		defer cancel()
		var raw json.RawMessage
		err := st.Do(ctx, func(ctx context.Context, svc docker.Service) error {
			var err error
			raw, err = fetch(ctx, svc)
			return err
		})
		if parent.Err() != nil {
			return nil // the view is gone
		}
		if err != nil {
			return inspectLoadedMsg{view: view, err: err, reason: st.ErrorText(err)}
		}
		lines, spans, err := prettyJSON(raw, containerState)
		if err != nil {
			return inspectLoadedMsg{view: view, err: err, reason: err.Error()}
		}
		return inspectLoadedMsg{view: view, lines: lines, spans: spans}
	}
}

// prettyJSON indents raw JSON, splits it into display-safe lines and scans
// each line for the spans that color it (see jsonScanner). containerState
// is withContainerState's setting. The spans hold token kinds, not styles,
// so they are scanned once and recolored on a palette change.
func prettyJSON(raw json.RawMessage, containerState bool) ([]string, [][]components.Span, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, nil, fmt.Errorf("invalid inspection data: %w", err)
	}
	lines := strings.Split(buf.String(), "\n")
	// All spans share one backing array; ends[i] is where line i's spans end.
	all := make([]components.Span, 0, 4*len(lines)) // about 4 spans per line
	ends := make([]int, len(lines))
	sc := jsonScanner{containerState: containerState}
	for i, l := range lines {
		// JSON strings may hold C1 controls and bidi characters unescaped.
		// Clean runs first, so the spans are offsets into the shown text.
		lines[i] = components.Clean(l)
		all = append(all, sc.line(lines[i])...)
		ends[i] = len(all)
	}
	spans := make([][]components.Span, len(lines))
	start := 0
	for i, end := range ends {
		spans[i] = all[start:end:end]
		start = end
	}
	return lines, spans, nil
}

func (v *inspectView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case inspectLoadedMsg:
		if msg.view != v.id {
			return nil
		}
		v.loading = false
		if msg.err != nil {
			v.errText = msg.reason
			return ui.Error("inspect " + v.name + ": " + msg.reason)
		}
		v.lines, v.spans = msg.lines, msg.spans
		width := 0
		for _, l := range v.lines {
			width = max(width, ansi.StringWidth(l))
		}
		v.pager.SetTotal(len(v.lines))
		v.pager.SetContentWidth(width)
	case tea.KeyPressMsg:
		v.pager.Update(msg)
	}
	return nil
}

func (v *inspectView) View() string {
	if v.width <= 0 || v.height <= 0 {
		return ""
	}
	var msg string
	switch {
	case v.loading:
		msg = "Loading…"
	case v.errText != "":
		msg = "Could not inspect " + v.name + ": " + v.errText
	default:
		return v.pager.ViewStyled(func(i int) (string, []components.Span) { return v.lines[i], v.spans[i] })
	}
	msg = components.Styles.Empty.Render(components.Truncate(components.Clean(msg), v.width))
	return lipgloss.Place(v.width, v.height, lipgloss.Center, lipgloss.Center, msg)
}

func (v *inspectView) SetSize(w, h int) {
	v.width, v.height = max(w, 0), max(h, 0)
	v.pager.SetSize(v.width, v.height)
}

func (v *inspectView) Crumbs() []string       { return []string{v.name, "inspect"} }
func (v *inspectView) Indicators() []string   { return nil }
func (v *inspectView) KeyHelp() []key.Binding { return keys.Pager.Bindings() }
func (v *inspectView) InputFocused() bool     { return false }
func (v *inspectView) Back() bool             { return false }
func (v *inspectView) Close()                 { v.cancel() }
