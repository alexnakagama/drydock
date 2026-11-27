//go:build integration

package views

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
	"github.com/alexnakagama/drydock/internal/store"
)

// dockerRunCompose runs `docker run -d` of alpine:latest with the labels
// Compose would set for service svc of project (one-off if oneOff), named
// name, plus drydock-it=1. Unlike dockerRun it does not pass --rm: a
// stopped container must outlive its command so that the project can be
// started again. --init makes the container exit promptly on SIGTERM, so a
// stop does not wait out the stop timeout. The container is removed when
// the test ends, if the test did not remove it; its ID is returned.
func dockerRunCompose(t *testing.T, project, svc, name, workingDir string, oneOff bool) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	args := []string{
		"run", "-d", "--init", "--name", name,
		"--label", "drydock-it=1",
		"--label", store.ComposeProjectLabel + "=" + project,
		"--label", store.ComposeServiceLabel + "=" + svc,
		"--label", store.ComposeOneoffLabel + "=" + composeBool(oneOff),
		"--label", store.ComposeWorkingDirLabel + "=" + workingDir,
		"alpine:latest", "sleep", "300",
	}
	cmd := exec.Command("docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker run %s: %v: %s", name, err, stderr.Bytes())
	}
	id := strings.TrimSpace(string(out))
	if err := docker.ValidateContainerID(id); err != nil {
		t.Fatalf("docker run printed %q, want a container ID: %v", out, err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	return id
}

// composeBool formats b the way Compose writes its one-off label.
func composeBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// projectContainers lists the IDs of every container, running or not,
// labeled with project, through the docker CLI rather than drydock.
func projectContainers(t *testing.T, project string) []string {
	t.Helper()
	out, err := exec.Command("docker", "ps", "-aq", "--no-trunc",
		"--filter", "label="+store.ComposeProjectLabel+"="+project).Output()
	if err != nil {
		t.Fatalf("docker ps for project %s: %v", project, err)
	}
	return strings.Fields(string(out))
}

// TestIntegrationProjectStopStart builds a Compose-like project on the real
// engine (two service containers and a one-off container, labeled as
// Compose labels them), then, through the root model: checks the project's
// row in the projects view, stops the project and starts it again, checking
// that the one-off container is left running, and finally removes the
// containers and checks that the project disappears.
func TestIntegrationProjectStopStart(t *testing.T) {
	// A name no developer project uses, so that the test never acts on a
	// real stack, and that parallel runs do not share; Compose project
	// names are lowercase.
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	project := "drydock-it-" + hex.EncodeToString(suffix)
	workingDir := testHome + "/src/" + project
	web := dockerRunCompose(t, project, "web", project+"-web-1", workingDir, false)
	db := dockerRunCompose(t, project, "db", project+"-db-1", workingDir, false)
	oneOff := dockerRunCompose(t, project, "web", project+"-web-run-1a2b", workingDir, true)

	svc, err := docker.NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	// Wide enough for the outcome report on the status line.
	e := newEnvOn(t, svc, nil, 200, 30)
	e.command("projects")
	if !strings.HasPrefix(e.header(), "drydock > projects") {
		t.Fatalf("projects view not shown; screen:\n%s", e.screen())
	}
	// The developer's own projects may fill the screen: list ours only.
	e.press("/")
	e.typeText(project)
	e.press("enter")

	row := func(running, glyph, state string) []string {
		return []string{project, running, glyph, state, "~/src/" + project}
	}
	e.until("project running", func() bool {
		return slices.Equal(rowFields(e.rowOf(project)), row("2/2", "●", "running"))
	})
	e.selectProject(project)

	running := func(id string) bool {
		t.Helper()
		res, err := svc.ContainerInspect(t.Context(), id)
		if err != nil {
			t.Fatalf("inspect %s: %v", id, err)
		}
		return res.Container.State != nil && res.Container.State.Running
	}

	e.press("s")
	e.waitStatus(fmt.Sprintf("stop %s: stopped 2 containers of %s", project, project))
	e.until("project exited", func() bool {
		return slices.Equal(rowFields(e.rowOf(project)), row("0/2", "○", "exited"))
	})
	if running(web) || running(db) {
		t.Error("a service container is still running after the project stop")
	}
	if !running(oneOff) {
		t.Error("the one-off container was stopped with the project")
	}

	// Start must skip an exited one-off container, as stop skipped a
	// running one.
	if out, err := exec.Command("docker", "stop", oneOff).CombinedOutput(); err != nil {
		t.Fatalf("docker stop of the one-off container: %v: %s", err, out)
	}
	e.until("one-off container exited", func() bool {
		p, ok := e.projectsView().selected()
		return ok && len(p.OneOffs) == 1 && p.OneOffs[0].State == container.StateExited
	})

	e.press("s")
	e.waitStatus(fmt.Sprintf("start %s: started 2 containers of %s", project, project))
	e.until("project running again", func() bool {
		return slices.Equal(rowFields(e.rowOf(project)), row("2/2", "●", "running"))
	})
	if !running(web) || !running(db) {
		t.Error("a service container is not running after the project start")
	}
	if running(oneOff) {
		t.Error("the one-off container was started with the project")
	}

	// Removing the labeled containers removes the project.
	if out, err := exec.Command("docker", "rm", "-f", web, db, oneOff).CombinedOutput(); err != nil {
		t.Fatalf("docker rm: %v: %s", err, out)
	}
	e.until("project gone", func() bool {
		return !slices.ContainsFunc(e.projectsView().snap.Projects(), func(p store.Project) bool {
			return p.Name == project
		})
	})
	if left := projectContainers(t, project); len(left) > 0 {
		t.Errorf("containers of %s left on the engine: %v", project, left)
	}
}
