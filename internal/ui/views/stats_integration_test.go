//go:build integration

package views

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/store"
)

// memCell is the shape of a MEM cell: usage and limit in binary units.
var memCell = regexp.MustCompile(`^[0-9.]+[KMGT]?i?B / [0-9.]+[KMGT]?i?B$`)

// TestIntegrationStats turns stats on in a containers view backed by the
// real engine and checks that a CPU-busy container gets a non-zero CPU cell
// and a well-formed MEM cell within 5 seconds, as the spec requires values
// to appear within a few seconds of stats being shown.
func TestIntegrationStats(t *testing.T) {
	// The busy loop is bounded by timeout, so a test binary killed before
	// its cleanup runs does not leave a container spinning on the host.
	id := dockerRun(t, "alpine:latest", "timeout", "-s", "KILL", "120", "sh", "-c", "while :; do :; done")

	svc, err := docker.NewSDKService("drydock-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	msgs := make(chan tea.Msg, 64)
	// send delivers every store message to the test goroutine, and gives up
	// once the test is over so that Run can return.
	send := func(m tea.Msg) {
		select {
		case msgs <- m:
		case <-ctx.Done():
		}
	}
	st := store.New(svc, send)
	done := make(chan struct{})
	go func() { defer close(done); _ = st.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = svc.Close() })

	// Wait for the store to connect with a snapshot listing the container.
	// session publishes the snapshot before it reports the connection.
	var snap store.Snapshot
	connectDeadline := time.After(10 * time.Second)
	for st.State() != store.StateConnected || !listsRunning(snap, id) {
		select {
		case m := <-msgs:
			if s, ok := m.(store.SnapshotMsg); ok {
				snap = s.Snapshot
			}
		case <-connectDeadline:
			t.Fatalf("store state %v, container listed %v", st.State(), listsRunning(snap, id))
		}
	}

	v := newContainersView(st, snap, &Prefs{Stats: true}, []string{"containers"}, "No containers.",
		nil, false, defaultExecEnv())
	t.Cleanup(v.Close) // runs before the store is stopped
	// run runs a view command as the root model would, delivering its
	// message to the test goroutine.
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		go func() {
			switch m := cmd().(type) {
			case nil:
			case tea.BatchMsg:
				for _, c := range m {
					run(c)
				}
			default:
				send(m)
			}
		}()
	}
	run(v.SetActive(true))

	c, ok := v.shown[id]
	if !ok {
		t.Fatalf("container %s not listed", store.ShortID(id))
	}
	var cpu, mem string
	deadline := time.After(5 * time.Second)
	for {
		cpu, mem = v.statsCells(c)
		if cpuAboveZero(cpu) && memCell.MatchString(mem) {
			break
		}
		select {
		case m := <-msgs:
			run(v.Update(m))
			if cc, ok := v.shown[id]; ok {
				c = cc
			}
		case <-deadline:
			t.Fatalf("after 5s: CPU %q, MEM %q; want CPU above 0.0%% and MEM matching %s", cpu, mem, memCell)
		}
	}
	t.Logf("CPU %s, MEM %s", cpu, mem)
}

// listsRunning reports whether snap lists container id as running.
func listsRunning(snap store.Snapshot, id string) bool {
	return slices.ContainsFunc(snap.Containers(), func(c container.Summary) bool {
		return c.ID == id && c.State == container.StateRunning
	})
}

// cpuAboveZero reports whether a CPU cell shows a percentage above 0.0%.
func cpuAboveZero(cell string) bool {
	s, ok := strings.CutSuffix(cell, "%")
	if !ok {
		return false
	}
	f, err := strconv.ParseFloat(s, 64)
	return err == nil && f > 0
}
