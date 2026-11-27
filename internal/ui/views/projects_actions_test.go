package views

import (
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
)

// svcCtr builds a service container of project myapp, named
// myapp-<svc>-1, with ID cid(n).
func svcCtr(n int, svc string, state container.ContainerState) container.Summary {
	return composeCtr(n, "myapp-"+svc+"-1", "alpine", state, string(state),
		dockerfake.Compose{Project: "myapp", Service: svc})
}

// oneOffCtr builds a one-off container of project myapp with ID cid(n).
func oneOffCtr(n int, state container.ContainerState) container.Summary {
	return composeCtr(n, "myapp-web-run-1a2b", "alpine", state, string(state),
		dockerfake.Compose{Project: "myapp", Service: "web", OneOff: true})
}

// myappEnv starts the projects view on an engine holding cs, with myapp
// selected and no calls recorded yet.
func myappEnv(t *testing.T, cs ...container.Summary) (*env, *dockerfake.Service) {
	t.Helper()
	f := dockerfake.New()
	for _, c := range cs {
		f.AddContainer(c)
	}
	// Wide enough for the longest outcome report on the status line.
	e := projectsEnv(t, f, 200, 14)
	e.selectProject("myapp")
	f.ResetCalls()
	return e, f
}

// calledIDs returns the container IDs m was called with, sorted.
func calledIDs(f *dockerfake.Service, m dockerfake.Method) []string {
	var ids []string
	for _, c := range f.CallsTo(m) {
		ids = append(ids, c.Args[0].(string))
	}
	slices.Sort(ids)
	return ids
}

// lifecycleMethods are the container calls a project action may make.
var lifecycleMethods = []dockerfake.Method{
	dockerfake.MethodContainerStart, dockerfake.MethodContainerStop, dockerfake.MethodContainerRestart,
	dockerfake.MethodContainerPause, dockerfake.MethodContainerUnpause, dockerfake.MethodContainerRemove,
}

// checkCalls fails unless the lifecycle calls made are exactly want
// (method to sorted container IDs).
func checkCalls(t *testing.T, f *dockerfake.Service, want map[dockerfake.Method][]string) {
	t.Helper()
	for _, m := range lifecycleMethods {
		if got := calledIDs(f, m); !slices.Equal(got, want[m]) {
			t.Errorf("%s called for %v, want %v", m, got, want[m])
		}
	}
}

func TestProjectStopRunning(t *testing.T) {
	e, f := myappEnv(t,
		svcCtr(0, "web", container.StateRunning),
		svcCtr(1, "db", container.StateRunning))
	e.press("s")
	e.waitStatus("stop myapp: stopped 2 containers of myapp")
	checkCalls(t, f, map[dockerfake.Method][]string{dockerfake.MethodContainerStop: {cid(0), cid(1)}})
	e.until("myapp exited", func() bool {
		return slices.Equal(rowFields(e.rowOf("myapp")), []string{"myapp", "0/2", "○", "exited", "-"})
	})
}

func TestProjectStopReportsCount(t *testing.T) {
	e, _ := myappEnv(t,
		svcCtr(0, "web", container.StateRunning),
		svcCtr(1, "db", container.StateRunning),
		svcCtr(2, "cache", container.StateRunning))
	e.press("s")
	e.waitStatus("stop myapp: stopped 3 containers of myapp")
}

func TestProjectStopPartlyRunning(t *testing.T) {
	e, f := myappEnv(t,
		svcCtr(0, "web", container.StateRunning),
		svcCtr(1, "worker", container.StateExited))
	e.press("s")
	e.waitStatus("stop myapp: stopped 1 container of myapp")
	checkCalls(t, f, map[dockerfake.Method][]string{dockerfake.MethodContainerStop: {cid(0)}})
}

func TestProjectStopLeavesPausedAndRestarting(t *testing.T) {
	e, f := myappEnv(t,
		svcCtr(0, "web", container.StateRunning),
		svcCtr(1, "db", container.StatePaused),
		svcCtr(2, "boot", container.StateRestarting))
	e.press("s")
	e.waitStatus("stop myapp: stopped 1 container of myapp")
	checkCalls(t, f, map[dockerfake.Method][]string{dockerfake.MethodContainerStop: {cid(0)}})
	e.settle()
	p, _ := e.projectsView().selected()
	for _, c := range p.Services {
		if c.ID == cid(1) && c.State != container.StatePaused {
			t.Errorf("db is %s, want paused", c.State)
		}
	}
}

func TestProjectStopSkipsOneOff(t *testing.T) {
	e, f := myappEnv(t,
		svcCtr(0, "web", container.StateRunning),
		oneOffCtr(1, container.StateRunning))
	e.press("s")
	e.waitStatus("stop myapp: stopped 1 container of myapp")
	checkCalls(t, f, map[dockerfake.Method][]string{dockerfake.MethodContainerStop: {cid(0)}})
	e.settle()
	if p, _ := e.projectsView().selected(); p.OneOffs[0].State != container.StateRunning {
		t.Errorf("one-off container is %s, want running", p.OneOffs[0].State)
	}
}

// TestProjectStopOneFails refuses the stop of myapp-db-1 only: the other
// container is still stopped and the error names the refused one with the
// engine's text.
func TestProjectStopOneFails(t *testing.T) {
	e, f := myappEnv(t,
		svcCtr(0, "web", container.StateRunning),
		svcCtr(1, "db", container.StateRunning))
	f.FailFor(dockerfake.MethodContainerStop, cid(1), cerrdefs.ErrConflict.WithMessage("cannot stop container: permission denied"))
	e.press("s")
	e.waitStatus("✗ stop myapp: 1 stopped, 1 failed (myapp-db-1): myapp-db-1: cannot stop container: permission denied")
	checkCalls(t, f, map[dockerfake.Method][]string{dockerfake.MethodContainerStop: {cid(0), cid(1)}})
	e.until("web stopped", func() bool {
		return slices.Equal(rowFields(e.rowOf("myapp"))[:4], []string{"myapp", "1/2", "◐", "partial"})
	})
}

func TestProjectStartStopped(t *testing.T) {
	e, f := myappEnv(t,
		svcCtr(0, "web", container.StateExited),
		svcCtr(1, "db", container.StateCreated),
		oneOffCtr(2, container.StateExited))
	e.press("s")
	e.waitStatus("start myapp: started 2 containers of myapp")
	checkCalls(t, f, map[dockerfake.Method][]string{dockerfake.MethodContainerStart: {cid(0), cid(1)}})
	e.until("myapp running", func() bool {
		return slices.Equal(rowFields(e.rowOf("myapp"))[:4], []string{"myapp", "2/2", "●", "running"})
	})
}

// TestProjectStartDoesNotRecreate starts a project whose db container was
// removed: only web is started, and nothing is created.
func TestProjectStartDoesNotRecreate(t *testing.T) {
	e, f := myappEnv(t,
		svcCtr(0, "web", container.StateExited),
		svcCtr(1, "db", container.StateExited))
	f.RemoveContainer(cid(1))
	e.until("db gone", func() bool {
		p, _ := e.projectsView().selected()
		return p.Total() == 1
	})
	f.ResetCalls()
	e.press("s")
	e.waitStatus("start myapp: started 1 container of myapp")
	// docker.Service has no container create call, so starting web by ID
	// is all that can happen: check nothing else was acted on and the
	// project still has one service container.
	checkCalls(t, f, map[dockerfake.Method][]string{dockerfake.MethodContainerStart: {cid(0)}})
	e.settle()
	if p, _ := e.projectsView().selected(); p.Total() != 1 {
		t.Errorf("myapp has %d service containers, want 1", p.Total())
	}
}

func TestProjectStartStopUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		cs   []container.Summary
		want string
	}{
		{"every service paused", []container.Summary{
			svcCtr(0, "web", container.StatePaused), svcCtr(1, "db", container.StatePaused),
		}, "start/stop is unavailable for project myapp: no service container is running, created or exited"},
		{"paused and restarting", []container.Summary{
			svcCtr(0, "web", container.StatePaused), svcCtr(1, "db", container.StateRestarting),
		}, "start/stop is unavailable for project myapp: no service container is running, created or exited"},
		{"only one-off containers", []container.Summary{
			oneOffCtr(0, container.StateRunning),
		}, "start/stop is unavailable for project myapp: it has no service container"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, f := myappEnv(t, tc.cs...)
			e.press("s")
			e.waitStatus(tc.want)
			e.settle()
			checkCalls(t, f, nil)
		})
	}
}

func TestProjectRestart(t *testing.T) {
	e, f := myappEnv(t,
		svcCtr(0, "web", container.StateRunning),
		svcCtr(1, "worker", container.StateExited),
		svcCtr(2, "boot", container.StateRestarting),
		oneOffCtr(3, container.StateRunning))
	e.press("r")
	e.waitStatus("restart myapp: restarted 3 containers of myapp")
	checkCalls(t, f, map[dockerfake.Method][]string{dockerfake.MethodContainerRestart: {cid(0), cid(1), cid(2)}})
	e.until("myapp running", func() bool {
		return slices.Equal(rowFields(e.rowOf("myapp"))[:4], []string{"myapp", "3/3", "●", "running"})
	})
}

func TestProjectRestartLeavesPaused(t *testing.T) {
	e, f := myappEnv(t,
		svcCtr(0, "web", container.StateRunning),
		svcCtr(1, "db", container.StatePaused))
	e.press("r")
	e.waitStatus("restart myapp: restarted 1 container of myapp")
	checkCalls(t, f, map[dockerfake.Method][]string{dockerfake.MethodContainerRestart: {cid(0)}})
}

func TestProjectRestartUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		cs   []container.Summary
		want string
	}{
		{"every service paused", []container.Summary{
			svcCtr(0, "web", container.StatePaused),
		}, "restart is unavailable for project myapp: every service container is paused"},
		{"only one-off containers", []container.Summary{
			oneOffCtr(0, container.StateExited),
		}, "restart is unavailable for project myapp: it has no service container"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, f := myappEnv(t, tc.cs...)
			e.press("r")
			e.waitStatus(tc.want)
			e.settle()
			checkCalls(t, f, nil)
		})
	}
}

func TestProjectActionsWithoutSelection(t *testing.T) {
	e, f := myappEnv(t, svcCtr(0, "web", container.StateRunning))
	e.press("/")
	e.typeText("zzz")
	e.press("enter")
	for k, want := range map[string]string{
		"s": "start/stop is unavailable: no project selected",
		"r": "restart is unavailable: no project selected",
	} {
		e.press(k)
		e.waitStatus(want)
	}
	e.settle()
	checkCalls(t, f, nil)
}

// TestProjectActionHostileName checks that a project name carrying
// terminal control sequences reaches neither the status line nor the
// progress indicator raw.
func TestProjectActionHostileName(t *testing.T) {
	f := dockerfake.New()
	const project = "evil\x1b[31m\x07"
	f.AddContainer(composeCtr(0, "evil-web-1", "nginx", container.StateRunning, "Up",
		dockerfake.Compose{Project: project, Service: "web"}))
	e := projectsEnv(t, f, 100, 10)
	e.press("s")
	e.waitStatus("stopped 1 container of evil")
	if raw := e.m.View().Content; strings.Contains(raw, "\x1b[31m") || strings.Contains(raw, "\x07") {
		t.Errorf("status line carries the engine's control sequence:\n%q", e.statusLine())
	}
}
