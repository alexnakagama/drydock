package views

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/store"
)

// sortRow is one container of a comparator test: its name, ID, state, and
// stats sample (nil for no sample in the batch).
type sortRow struct {
	name, id string
	state    container.ContainerState
	stats    *store.ContainerStats
}

func cpuStats(pct float64) *store.ContainerStats {
	return &store.ContainerStats{CPUPercent: pct, HasCPU: true, MemUsage: 1, MemLimit: gib, HasMem: true}
}

func memStats(usage uint64) *store.ContainerStats {
	return &store.ContainerStats{CPUPercent: 1, HasCPU: true, MemUsage: usage, MemLimit: 4 * gib, HasMem: true}
}

func running(name string, s *store.ContainerStats) sortRow {
	return sortRow{name: name, id: "id-" + name, state: container.StateRunning, stats: s}
}

// sortedNames sorts rows with containerOrder and returns their names in
// the resulting order. Each permutation of the input must give the same
// order, so the result does not depend on where a row started.
func sortedNames(t *testing.T, order sortOrder, conn store.State, rows []sortRow) []string {
	t.Helper()
	stats := map[string]store.ContainerStats{}
	list := make([]container.Summary, len(rows))
	for i, r := range rows {
		list[i] = container.Summary{ID: r.id, Names: []string{"/" + r.name}, State: r.state}
		if r.stats != nil {
			stats[r.id] = *r.stats
		}
	}
	cmpFn := containerOrder(order, conn, stats)
	names := func(l []container.Summary) []string {
		out := make([]string, len(l))
		for i, c := range l {
			out[i] = store.ContainerName(c)
		}
		return out
	}

	first := slices.Clone(list)
	slices.SortFunc(first, cmpFn)
	want := names(first)
	rng := rand.New(rand.NewPCG(1, 2))
	for range 20 {
		perm := slices.Clone(list)
		rng.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
		slices.SortFunc(perm, cmpFn)
		if got := names(perm); !slices.Equal(got, want) {
			t.Fatalf("order depends on the input order: %v, then %v", want, got)
		}
	}
	return want
}

func TestSortOrder(t *testing.T) {
	connected := store.StateConnected
	tests := []struct {
		name  string
		order sortOrder
		conn  store.State
		rows  []sortRow
		want  []string
	}{
		{
			name:  "name order ignores stats",
			order: sortName,
			conn:  connected,
			rows:  []sortRow{running("web", cpuStats(90)), running("api", cpuStats(1)), running("db", nil)},
			want:  []string{"api", "db", "web"},
		},
		{
			name:  "largest CPU first",
			order: sortCPU,
			conn:  connected,
			rows:  []sortRow{running("db", cpuStats(2.1)), running("worker", cpuStats(38)), running("api", cpuStats(41.2))},
			want:  []string{"api", "worker", "db"},
		},
		{
			name:  "CPU above 100 percent (several cores)",
			order: sortCPU,
			conn:  connected,
			rows:  []sortRow{running("one", cpuStats(99.9)), running("two", cpuStats(200))},
			want:  []string{"two", "one"},
		},
		{
			name:  "largest MEM first",
			order: sortMem,
			conn:  connected,
			rows: []sortRow{
				running("web", memStats(20*mib)), running("db", memStats(12*gib/10)), running("api", memStats(300*mib)),
			},
			want: []string{"db", "api", "web"},
		},
		{
			name:  "MEM compares bytes, not the shown unit",
			order: sortMem,
			conn:  connected,
			rows:  []sortRow{running("a", memStats(1000)), running("b", memStats(1001))},
			want:  []string{"b", "a"},
		},
		{
			name:  "0.04% and 0.0% compare equal and fall back to name",
			order: sortCPU,
			conn:  connected,
			rows:  []sortRow{running("web", cpuStats(0.04)), running("cache", cpuStats(0))},
			want:  []string{"cache", "web"},
		},
		{
			name:  "0.25 orders as the 0.2% its cell shows",
			order: sortCPU,
			conn:  connected,
			// 0.25 shows as 0.2%, equal to b's 0.2%, so name order; a
			// math.Round of 0.25*10 would put z (as 0.3) first.
			rows: []sortRow{running("z", cpuStats(0.25)), running("b", cpuStats(0.2)), running("c", cpuStats(0.3))},
			want: []string{"c", "b", "z"},
		},
		{
			name:  "rows equal in every key are in name, then ID, order",
			order: sortCPU,
			conn:  connected,
			rows: []sortRow{
				{name: "dup", id: "id-2", state: container.StateRunning, stats: cpuStats(5)},
				{name: "dup", id: "id-1", state: container.StateRunning, stats: cpuStats(5)},
				{name: "alpha", id: "id-3", state: container.StateRunning, stats: cpuStats(5)},
			},
			want: []string{"alpha", "dup", "dup"},
		},
		{
			name:  "placeholders last, in name order, for CPU",
			order: sortCPU,
			conn:  connected,
			rows: []sortRow{
				{name: "old", id: "id-old", state: container.StateExited},
				{name: "frozen", id: "id-frozen", state: container.StatePaused, stats: cpuStats(50)},
				running("fresh", nil),
				running("nocpu", &store.ContainerStats{MemUsage: gib, MemLimit: gib, HasMem: true}),
				running("web", cpuStats(0)),
			},
			want: []string{"web", "fresh", "frozen", "nocpu", "old"},
		},
		{
			name:  "HasMem=false and other placeholders last for MEM",
			order: sortMem,
			conn:  connected,
			rows: []sortRow{
				running("api", &store.ContainerStats{CPUPercent: 90, HasCPU: true}),
				{name: "old", id: "id-old", state: container.StateExited},
				running("tiny", memStats(1)),
				running("fresh", nil),
				running("big", memStats(gib)),
			},
			want: []string{"big", "tiny", "api", "fresh", "old"},
		},
		{
			name:  "disconnected puts every row in name order",
			order: sortCPU,
			conn:  store.StateDisconnected,
			rows:  []sortRow{running("web", cpuStats(90)), running("api", cpuStats(1)), running("db", cpuStats(50))},
			want:  []string{"api", "db", "web"},
		},
		{
			name:  "disconnected MEM puts every row in name order",
			order: sortMem,
			conn:  store.StateDisconnected,
			rows:  []sortRow{running("web", memStats(gib)), running("api", memStats(1)), running("db", nil)},
			want:  []string{"api", "db", "web"},
		},
		{
			name:  "MEM above 2^53 bytes compares exactly",
			order: sortMem,
			conn:  connected,
			rows:  []sortRow{running("a", memStats(1<<53+1)), running("b", memStats(1<<53+2))},
			want:  []string{"b", "a"},
		},
		{
			name:  "NaN and infinite CPU keep a total order",
			order: sortCPU,
			conn:  connected,
			rows: []sortRow{
				running("nan", cpuStats(math.NaN())), running("inf", cpuStats(math.Inf(1))),
				running("five", cpuStats(5)), running("neg", cpuStats(math.Inf(-1))), running("zero", cpuStats(0)),
			},
			want: []string{"inf", "five", "zero", "neg", "nan"},
		},
		{
			name:  "-0.0% and 0.0% compare equal and fall back to name",
			order: sortCPU,
			conn:  connected,
			rows:  []sortRow{running("web", cpuStats(-0.04)), running("cache", cpuStats(0))},
			want:  []string{"cache", "web"},
		},
		{
			name:  "unknown order sorts by name",
			order: sortOrder(99),
			conn:  connected,
			rows:  []sortRow{running("web", cpuStats(90)), running("api", cpuStats(1))},
			want:  []string{"api", "web"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sortedNames(t, tt.order, tt.conn, tt.rows); !slices.Equal(got, tt.want) {
				t.Errorf("order = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSortCPUMatchesCell checks that two CPU values compare equal exactly
// when their cells show the same number, for values around rounding halves.
// "-0.0%" and "0.0%" count as the same number.
func TestSortCPUMatchesCell(t *testing.T) {
	values := []float64{-0.04, 0, 0.04, 0.05, 0.0500001, 0.15, 0.25, 0.35, 1.25, 2.45, 99.95, 100, 199.99}
	cell := func(v float64) string {
		if s := formatCPU(v); s != "-0.0%" {
			return s
		}
		return "0.0%"
	}
	for _, a := range values {
		for _, b := range values {
			sameCell := cell(a) == cell(b)
			sameKey := shownCPU(a) == shownCPU(b)
			if sameCell != sameKey {
				t.Errorf("%v (%s) vs %v (%s): equal cells %v, equal sort keys %v",
					a, formatCPU(a), b, formatCPU(b), sameCell, sameKey)
			}
			if !sameCell && (shownCPU(a) < shownCPU(b)) != (a < b) {
				t.Errorf("%v vs %v: sort key order disagrees with value order", a, b)
			}
		}
	}
}
