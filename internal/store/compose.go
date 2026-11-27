package store

import (
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
)

// Labels Docker Compose sets on every container it creates. drydock reads
// them to group containers into projects; it never writes them.
const (
	// ComposeProjectLabel holds the Compose project name.
	ComposeProjectLabel = "com.docker.compose.project"
	// ComposeServiceLabel holds the service the container belongs to.
	ComposeServiceLabel = "com.docker.compose.service"
	// ComposeOneoffLabel is "True" on containers created by
	// `docker compose run` and "False" on service containers.
	ComposeOneoffLabel = "com.docker.compose.oneoff"
	// ComposeWorkingDirLabel holds the project's working directory on the
	// host that ran Compose.
	ComposeWorkingDirLabel = "com.docker.compose.project.working_dir"
)

// ProjectState summarizes the states of a project's service containers.
type ProjectState int

// Project states, see Project.State.
const (
	// ProjectExited: no service container, or every one is exited,
	// created or dead.
	ProjectExited ProjectState = iota
	// ProjectPartial: some service containers are not running, and at
	// least one is neither exited, created nor dead (for example paused).
	ProjectPartial
	// ProjectRunning: every service container is running.
	ProjectRunning
)

// String returns the state's name as the projects view displays it.
func (s ProjectState) String() string {
	switch s {
	case ProjectRunning:
		return "running"
	case ProjectPartial:
		return "partial"
	default:
		return "exited"
	}
}

// Project summarizes one Compose project: the containers, running or
// stopped, that carry the same non-empty ComposeProjectLabel.
//
// Name and WorkingDir are raw label values from the engine and are
// untrusted: the UI must pass them through docker.SanitizeText before
// rendering. Name is also the exact label value to match containers by.
type Project struct {
	Name string
	// Services are the project's service containers, sorted by name then
	// ID: every container not marked as one-off.
	Services []container.Summary
	// OneOffs are the containers created by `docker compose run`, sorted by
	// name then ID. They belong to the project but are not counted in
	// Running or State, and project actions do not act on them.
	OneOffs []container.Summary
	// Running is the number of service containers in the running state.
	// Paused and restarting containers are not running.
	Running int
	// State is derived from the service containers' states.
	State ProjectState
	// WorkingDir is the ComposeWorkingDirLabel of the first container, in
	// name order, that records one, or "" if none does.
	WorkingDir string
}

// Total returns the number of service containers, the denominator of the
// RUNNING count.
func (p Project) Total() int { return len(p.Services) }

// Projects returns a summary of every Compose project, sorted by name.
// Each summary's container slices are fresh copies, but like Containers()
// the container.Summary elements are shallow: their maps and slices (for
// example Labels and Names) are shared with the snapshot and must not be
// modified.
func (s Snapshot) Projects() []Project {
	out := make([]Project, 0, len(s.projectNames))
	for _, name := range s.projectNames {
		out = append(out, summarizeProject(name, s.projects[name]))
	}
	return out
}

// NumProjects returns len(Projects()) without building the summaries.
func (s Snapshot) NumProjects() int { return len(s.projectNames) }

// IsOneoff reports whether c was created by `docker compose run`: its
// ComposeOneoffLabel is "true" in any case. Any other value, or no label,
// marks a service container.
func IsOneoff(c container.Summary) bool {
	return strings.EqualFold(c.Labels[ComposeOneoffLabel], "true")
}

// buildProjects groups containers by their non-empty ComposeProjectLabel,
// used verbatim as the key (whitespace is not trimmed; Compose itself never
// writes such names).
// Each project's containers are sorted by name then ID, and names lists
// the projects sorted by name. Containers without the label, or with an
// empty value, belong to no project.
func buildProjects(containers []container.Summary) (projects map[string][]container.Summary, names []string) {
	projects = make(map[string][]container.Summary)
	for _, c := range containers {
		if name := c.Labels[ComposeProjectLabel]; name != "" {
			projects[name] = append(projects[name], c)
		}
	}
	names = make([]string, 0, len(projects))
	for name, cs := range projects {
		sortContainers(cs)
		names = append(names, name)
	}
	slices.Sort(names)
	return projects, names
}

// summarizeProject classifies a project's containers, already sorted by
// name then ID, and derives its running count, state and working dir.
func summarizeProject(name string, cs []container.Summary) Project {
	p := Project{Name: name}
	var stopped int // service containers that are exited, created or dead
	for _, c := range cs {
		if p.WorkingDir == "" {
			p.WorkingDir = c.Labels[ComposeWorkingDirLabel]
		}
		if IsOneoff(c) {
			p.OneOffs = append(p.OneOffs, c)
			continue
		}
		p.Services = append(p.Services, c)
		switch c.State {
		case container.StateRunning:
			p.Running++
		case container.StateExited, container.StateCreated, container.StateDead:
			stopped++
		}
	}
	switch total := len(p.Services); {
	case stopped == total: // includes a project with no service container
		p.State = ProjectExited
	case p.Running == total:
		p.State = ProjectRunning
	default:
		p.State = ProjectPartial
	}
	return p
}
