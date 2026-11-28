package views

import (
	"errors"
	"image/color"
	"math"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui/components"
)

func TestCPULevel(t *testing.T) {
	t.Parallel()
	cpu := func(pct float64, cpus int) store.ContainerStats {
		return store.ContainerStats{CPUPercent: pct, CPUs: cpus, HasCPU: true}
	}
	for _, tt := range []struct {
		name      string
		s         store.ContainerStats
		wantLevel float64
		wantOK    bool
		wantTone  components.Tone
	}{
		{"280% on 4 cores is warn", cpu(280, 4), 0.7, true, components.ToneWarn},
		{"100% on 16 cores keeps the row tone", cpu(100, 16), 0.0625, true, components.ToneDefault},
		{"92% on 1 core is bad", cpu(92, 1), 0.92, true, components.ToneBad},
		{"just below warn", cpu(279.9, 4), 0.69975, true, components.ToneDefault},
		{"exactly 90% is bad", cpu(360, 4), 0.9, true, components.ToneBad},
		{"just below bad", cpu(359.9, 4), 0.89975, true, components.ToneWarn},
		// The level follows the value the cell shows: 279.96 reads 280.0%.
		{"rounded as shown", cpu(279.96, 4), 0.7, true, components.ToneWarn},
		{"above capacity", cpu(1600, 4), 4, true, components.ToneBad},
		{"idle", cpu(0, 4), 0, true, components.ToneDefault},
		{"no CPU value", store.ContainerStats{CPUPercent: 95, CPUs: 1}, 0, false, components.ToneDefault},
		{"no CPU count", cpu(95, 0), 0, false, components.ToneDefault},
		{"negative CPU count", cpu(95, -1), 0, false, components.ToneDefault},
		{"NaN", cpu(math.NaN(), 4), 0, false, components.ToneDefault},
		{"+Inf", cpu(math.Inf(1), 4), math.Inf(1), true, components.ToneBad},
		{"-Inf", cpu(math.Inf(-1), 4), math.Inf(-1), true, components.ToneDefault},
		{"negative", cpu(-50, 4), -0.125, true, components.ToneDefault},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, ok := cpuLevel(tt.s)
			if ok != tt.wantOK || !levelNear(l, tt.wantLevel) {
				t.Errorf("cpuLevel = %v, %v; want %v, %v", l, ok, tt.wantLevel, tt.wantOK)
			}
			if got := usageTone(l, ok); got != tt.wantTone {
				t.Errorf("tone %v, want %v", got, tt.wantTone)
			}
		})
	}
}

func TestMemLevel(t *testing.T) {
	t.Parallel()
	mem := func(usage, limit uint64) store.ContainerStats {
		return store.ContainerStats{MemUsage: usage, MemLimit: limit, HasMem: true}
	}
	for _, tt := range []struct {
		name      string
		s         store.ContainerStats
		wantLevel float64
		wantOK    bool
		wantTone  components.Tone
	}{
		{"922 MiB of 1 GiB is bad", mem(922*mib, gib), 922.0 / 1024, true, components.ToneBad},
		{"700 of 1000 is warn", mem(700, 1000), 0.7, true, components.ToneWarn},
		{"699 of 1000 keeps the row tone", mem(699, 1000), 0.699, true, components.ToneDefault},
		{"900 of 1000 is bad", mem(900, 1000), 0.9, true, components.ToneBad},
		{"899 of 1000 is warn", mem(899, 1000), 0.899, true, components.ToneWarn},
		{"a quarter", mem(256*mib, gib), 0.25, true, components.ToneDefault},
		{"empty", mem(0, gib), 0, true, components.ToneDefault},
		{"over the limit clamps to 1", mem(3*gib, gib), 1, true, components.ToneBad},
		{"max values", mem(math.MaxUint64, math.MaxUint64), 1, true, components.ToneBad},
		{"zero limit", mem(mib, 0), 0, false, components.ToneDefault},
		{"no memory value", store.ContainerStats{MemUsage: 900, MemLimit: 1000}, 0, false, components.ToneDefault},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, ok := memLevel(tt.s)
			if ok != tt.wantOK || !levelNear(l, tt.wantLevel) {
				t.Errorf("memLevel = %v, %v; want %v, %v", l, ok, tt.wantLevel, tt.wantOK)
			}
			if got := usageTone(l, ok); got != tt.wantTone {
				t.Errorf("tone %v, want %v", got, tt.wantTone)
			}
		})
	}
}

// levelNear reports whether level got is want, within rounding; infinite
// levels must match exactly.
func levelNear(got, want float64) bool {
	if math.IsInf(want, 0) {
		return got == want
	}
	return math.Abs(got-want) <= 1e-12
}

func TestUsageToneLevelBoundaries(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		level float64
		ok    bool
		want  components.Tone
	}{
		{0, true, components.ToneDefault},
		{math.Nextafter(0.7, 0), true, components.ToneDefault},
		{0.7, true, components.ToneWarn},
		{math.Nextafter(0.9, 0), true, components.ToneWarn},
		{0.9, true, components.ToneBad},
		{1, true, components.ToneBad},
		{-1, true, components.ToneDefault},
		{math.NaN(), true, components.ToneDefault},
		{0.95, false, components.ToneDefault},
	} {
		if got := usageTone(tt.level, tt.ok); got != tt.want {
			t.Errorf("usageTone(%v, %v) = %v, want %v", tt.level, tt.ok, got, tt.want)
		}
	}
}

// styleOpen returns the escape sequence st opens its text with, failing the
// test if st adds none (the check would then pass on unstyled output).
func styleOpen(t *testing.T, st lipgloss.Style) string {
	t.Helper()
	open, _, ok := strings.Cut(st.Render("x"), "x")
	if !ok || open == "" {
		t.Fatalf("style renders no escape sequence: %q", st.Render("x"))
	}
	return open
}

// rawRowOf returns the unstripped screen line listing name as its first
// cell, or "".
func (e *env) rawRowOf(name string) string {
	for _, l := range strings.Split(e.m.View().Content, "\n") {
		if f := rowFields(ansi.Strip(l)); len(f) > 0 && f[0] == name {
			return l
		}
	}
	return ""
}

// rowFor returns the row the view builds for name.
func rowFor(t *testing.T, v *containersView, name string) components.Row {
	t.Helper()
	for i, r := range v.rows() {
		if store.ContainerName(v.list[i]) == name {
			return r
		}
	}
	t.Fatalf("no row %s", name)
	return components.Row{}
}

// cellTone returns r's tone for cell i: its CellTones entry, or ToneDefault.
func cellTone(r components.Row, i int) components.Tone {
	if i < len(r.CellTones) {
		return r.CellTones[i]
	}
	return components.ToneDefault
}

// statsTones returns the tones of r's CPU and MEM cells, which are the last
// two cells in every stats layout, and fails the test if r has more tones
// than cells.
func statsTones(t *testing.T, r components.Row) (cpu, mem components.Tone) {
	t.Helper()
	n := len(r.Cells)
	if n < 2 || len(r.CellTones) > n {
		t.Fatalf("row %s: %d cells, %d cell tones", r.Key, n, len(r.CellTones))
	}
	return cellTone(r, n-2), cellTone(r, n-1)
}

// TestContainersStatsUsageTones checks that CPU and MEM cells take their
// usage tones and the MEM meter in running rows, and only there.
func TestContainersStatsUsageTones(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 130, 12)
	e.press("a", "t")
	e.waitStreams(2)
	e.selectRow("frozen") // web and db are unselected

	f.SetStats(ids["web"], statsSample(10, 750*mib, gib)) // MEM 73%: warn
	f.SetStats(ids["db"], statsSample(380, 100*mib, gib)) // CPU 95% of 4 cores: bad
	e.until("values", func() bool {
		web, _ := e.statsCellsOf("web")
		db, _ := e.statsCellsOf("db")
		return web == "10.0%" && db == "380.0%"
	})
	v := e.root()

	web := rowFor(t, v, "web")
	if cpu, mem := statsTones(t, web); mem != components.ToneWarn || cpu != components.ToneDefault {
		t.Errorf("web cell tones %v, want MEM warn only", web.CellTones)
	}
	if !web.HasMeter || math.Abs(web.Meter-750.0/1024) > 1e-12 {
		t.Errorf("web meter %v %v, want 750/1024", web.Meter, web.HasMeter)
	}
	db := rowFor(t, v, "db")
	if cpu, mem := statsTones(t, db); cpu != components.ToneBad || mem != components.ToneDefault {
		t.Errorf("db cell tones %v, want CPU bad only", db.CellTones)
	}
	if web.Tone != components.ToneGood || db.Tone != components.ToneGood {
		t.Errorf("row tones %v %v, want good", web.Tone, db.Tone)
	}

	warn, bad, good := styleOpen(t, components.Styles.RowWarn), styleOpen(t, components.Styles.RowBad),
		styleOpen(t, components.Styles.RowGood)
	if raw := e.rawRowOf("web"); !strings.Contains(raw, warn+"750MiB / 1GiB") || !strings.Contains(raw, good+"web") {
		t.Errorf("web line lacks a warn MEM cell in a good row: %q", raw)
	}
	if raw := e.rawRowOf("db"); !strings.Contains(raw, bad+"380.0%") || strings.Contains(raw, warn) {
		t.Errorf("db line lacks a bad CPU cell, or has a warn one: %q", raw)
	}

	// Exited and paused rows have no tone and no meter, even with values at
	// hand.
	batch := map[string]store.ContainerStats{}
	for _, id := range ids {
		batch[id] = store.ContainerStats{CPUPercent: 390, CPUs: 4, HasCPU: true, MemUsage: gib, MemLimit: gib, HasMem: true}
	}
	v.Update(statsMsg{view: v.id, sub: v.sub, stats: batch})
	for _, name := range []string{"old", "frozen"} {
		if r := rowFor(t, v, name); r.CellTones != nil || r.HasMeter {
			t.Errorf("%s: cell tones %v, meter %v; want none", name, r.CellTones, r.HasMeter)
		}
	}
	if raw := e.rawRowOf("old"); strings.Contains(raw, bad) || strings.Contains(raw, "█") || strings.Contains(raw, "░") {
		t.Errorf("exited old line has a usage color or bar: %q", raw)
	}
	if cpu, mem := statsTones(t, rowFor(t, v, "web")); cpu != components.ToneBad || mem != components.ToneBad {
		t.Errorf("web at 97.5%% CPU and full memory: CPU %v, MEM %v; want both bad", cpu, mem)
	}

	// Disconnected: no tone and no bar anywhere.
	f.Disconnect(errors.New("engine gone"))
	e.until("disconnected", func() bool { return e.root().conn == store.StateDisconnected })
	for _, r := range v.rows() {
		if r.CellTones != nil || r.HasMeter {
			t.Errorf("row %s while disconnected: cell tones %v, meter %v; want none", r.Key, r.CellTones, r.HasMeter)
		}
	}
	if s := e.screen(); strings.ContainsAny(s, "█░") {
		t.Errorf("bars shown while disconnected:\n%s", s)
	}
}

// TestContainersStatsUsageTonesLightPalette checks that usage colors follow
// the palette: the light one's warn and error colors after a light
// background report. components.Styles is a package global, so this test
// must stay serial.
func TestContainersStatsUsageTonesLightPalette(t *testing.T) {
	saved := components.Styles
	t.Cleanup(func() { components.Styles = saved })

	f, ids := sample()
	e := newEnv(t, f, 130, 12)
	e.press("t")
	e.waitStreams(2)
	e.selectRow("frozen")
	f.SetStats(ids["web"], statsSample(10, 820*mib, gib)) // MEM 80%: warn
	f.SetStats(ids["db"], statsSample(380, 100*mib, gib)) // CPU 95%: bad
	e.until("values", func() bool {
		web, _ := e.statsCellsOf("web")
		db, _ := e.statsCellsOf("db")
		return web == "10.0%" && db == "380.0%"
	})

	dark, light := components.NewStyles(true), components.NewStyles(false)
	e.deliver(tea.BackgroundColorMsg{Color: color.White})
	lightWarn, lightBad := styleOpen(t, light.RowWarn), styleOpen(t, light.RowBad)
	if lightWarn == styleOpen(t, dark.RowWarn) || lightBad == styleOpen(t, dark.RowBad) {
		t.Fatal("light and dark palettes share the warn or error color")
	}
	if raw := e.rawRowOf("web"); !strings.Contains(raw, lightWarn+"820MiB / 1GiB") {
		t.Errorf("web MEM not in the light warning color: %q", raw)
	}
	if raw := e.rawRowOf("db"); !strings.Contains(raw, lightBad+"380.0%") {
		t.Errorf("db CPU not in the light error color: %q", raw)
	}
	raw := e.m.View().Content
	if strings.Contains(raw, styleOpen(t, dark.RowWarn)) || strings.Contains(raw, styleOpen(t, dark.RowBad)) {
		t.Errorf("dark warn or error color still shown after a light report:\n%q", raw)
	}
}

// barStart returns the cell column where the bar in line starts, or -1 if
// line has none.
func barStart(line string) int {
	i := strings.IndexAny(line, "█▏▎▍▌▋▊▉░")
	if i < 0 {
		return -1
	}
	// The gutter's selection marker is a ▌ too: skip it.
	if i == 0 {
		j := strings.IndexAny(line[len("▌"):], "█▏▎▍▌▋▊▉░")
		if j < 0 {
			return -1
		}
		i = len("▌") + j
	}
	return ansi.StringWidth(line[:i])
}

// checkAlignedBars checks that the rows of names each show a bar, all
// starting at the same column, and that the rows of none show no bar.
func (e *env) checkAlignedBars(names, none []string) {
	e.t.Helper()
	col := -1
	for _, n := range names {
		row := e.rowOf(n)
		got := barStart(row)
		switch {
		case got < 0:
			e.t.Errorf("%s shows no bar: %q", n, row)
		case col < 0:
			col = got
		case got != col:
			e.t.Errorf("%s bar starts at column %d, want %d: %q", n, got, col, row)
		}
	}
	for _, n := range none {
		if row := e.rowOf(n); barStart(row) >= 0 {
			e.t.Errorf("%s shows a bar: %q", n, row)
		}
	}
}

func TestContainersStatsMetersGolden(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 130, 10)
	e.press("a", "t")
	e.waitStreams(2)
	f.SetStats(ids["web"], statsSample(12.5, 256*mib, gib))     // 25%
	f.SetStats(ids["db"], statsSample(380, mibs(999.9), 2*gib)) // 48.8%
	e.until("values", func() bool {
		web, _ := e.statsCellsOf("web")
		db, _ := e.statsCellsOf("db")
		return web != noStats && db != noStats
	})
	checkGolden(t, "containers_stats_meters_130x10.golden", e.screen())
	e.checkAlignedBars([]string{"db", "web"}, []string{"old", "frozen"})
	if row := e.rowOf("web"); !strings.Contains(row, "256MiB / 1GiB    ██▌░░░░░░░") {
		t.Errorf("web row %q lacks its quarter bar after the widest MEM text", row)
	}
}

func TestContainersComposeStatsMetersGolden(t *testing.T) {
	f := composeSample()
	e := newEnv(t, f, 130, 10)
	e.press("t")
	e.waitStreams(3)
	f.SetStats(cid(0), statsSample(12.5, mibs(512.3), 2*gib))
	f.SetStats(cid(1), statsSample(1600, mibs(999.9), 2*gib))
	f.SetStats(cid(3), statsSample(0.4, mibs(8.1), gib))
	e.until("values", func() bool {
		for _, n := range []string{"web", "db", "redis"} {
			if cpu, _ := e.statsCellsOf(n); cpu == noStats {
				return false
			}
		}
		return true
	})
	checkGolden(t, "containers_compose_stats_meters_130x10.golden", e.screen())
	e.checkAlignedBars([]string{"db", "redis", "web"}, nil)
}

// TestContainersComposeStatsUsageTones checks that with the Compose column
// the usage tones land on the CPU and MEM cells, not the column before them.
func TestContainersComposeStatsUsageTones(t *testing.T) {
	f := composeSample()
	e := newEnv(t, f, 130, 10)
	e.press("t")
	e.waitStreams(3)
	e.selectRow("redis")                               // web and db are unselected
	f.SetStats(cid(0), statsSample(10, 750*mib, gib))  // web: MEM 73%, warn
	f.SetStats(cid(1), statsSample(380, 100*mib, gib)) // db: CPU 95%, bad
	e.until("values", func() bool {
		web, _ := e.statsCellsOf("web")
		db, _ := e.statsCellsOf("db")
		return web == "10.0%" && db == "380.0%"
	})
	v := e.root()
	if !v.composeCols {
		t.Fatal("view lacks the Compose column")
	}
	web, db := rowFor(t, v, "web"), rowFor(t, v, "db")
	if len(web.Cells) != len(composeStatsColumns) {
		t.Fatalf("web has %d cells, want %d", len(web.Cells), len(composeStatsColumns))
	}
	if want := []components.Tone{0, 0, 0, 0, components.ToneDefault, components.ToneWarn}; !slices.Equal(web.CellTones, want) {
		t.Errorf("web cell tones %v, want %v", web.CellTones, want)
	}
	if want := []components.Tone{0, 0, 0, 0, components.ToneBad, components.ToneDefault}; !slices.Equal(db.CellTones, want) {
		t.Errorf("db cell tones %v, want %v", db.CellTones, want)
	}
	warn, bad := styleOpen(t, components.Styles.RowWarn), styleOpen(t, components.Styles.RowBad)
	if raw := e.rawRowOf("web"); !strings.Contains(raw, warn+"750MiB / 1GiB") {
		t.Errorf("web MEM not in the warning color: %q", raw)
	}
	if raw := e.rawRowOf("db"); !strings.Contains(raw, bad+"380.0%") {
		t.Errorf("db CPU not in the error color: %q", raw)
	}
}

// TestContainersStatsMetersKeepText checks, at 100 columns, that usage
// colors only add styling: the warn and error colors are on screen, no bar
// is drawn however short the MEM text, and the stripped table is the one
// drawn without usage colors and meters.
func TestContainersStatsMetersKeepText(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 10)
	e.press("t")
	e.waitStreams(2)
	e.selectRow("frozen")
	f.SetStats(ids["web"], statsSample(300, 52*kib, gib)) // CPU 75%: warn
	f.SetStats(ids["db"], statsSample(380, 52*kib, gib))  // CPU 95%: bad
	e.until("values", func() bool {
		web, _ := e.statsCellsOf("web")
		db, _ := e.statsCellsOf("db")
		return web == "300.0%" && db == "380.0%"
	})

	raw := e.m.View().Content
	warn, bad := styleOpen(t, components.Styles.RowWarn), styleOpen(t, components.Styles.RowBad)
	if !strings.Contains(raw, warn+"300.0%") || !strings.Contains(raw, bad+"380.0%") {
		t.Errorf("screen lacks the warn or error CPU cell:\n%q", raw)
	}
	stripped := ansi.Strip(raw)
	if !strings.Contains(stripped, "52KiB / 1GiB") {
		t.Fatalf("screen lacks the short MEM text:\n%s", stripped)
	}
	for _, n := range []string{"web", "db"} {
		if row := e.rowOf(n); barStart(row) >= 0 {
			t.Errorf("%s shows a bar at 100 columns: %q", n, row)
		}
	}

	// The same rows without tones and meters, in columns without a meter.
	v := e.root()
	got := v.View()
	lines := strings.Split(got, "\n")
	cols := slices.Clone(columnsFor(true, false, composeProject))
	for i := range cols {
		cols[i].Meter = false
	}
	rows := v.rows()
	for i := range rows {
		rows[i].CellTones, rows[i].Meter, rows[i].HasMeter = nil, 0, false
	}
	plain := components.NewTable(cols)
	plain.SetSize(ansi.StringWidth(lines[0]), len(lines))
	plain.SetRows(rows)
	if sel, ok := v.table.Selected(); !ok || !plain.Select(sel.Key) {
		t.Fatal("cannot select the view's row in the plain table")
	}
	if want := ansi.Strip(plain.View()); ansi.Strip(got) != want {
		t.Errorf("stripped table differs from one without usage colors:\n--- got ---\n%s\n--- want ---\n%s", ansi.Strip(got), want)
	}
}
