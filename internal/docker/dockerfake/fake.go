// Package dockerfake provides an in-memory docker.Service for tests.
//
// The fake keeps containers, images and volumes in memory and applies the
// same rules the engine does for the operations drydock uses (for example,
// a running container cannot be removed without force, and removing an
// image reference that shares its image with other tags only untags it).
// Mutations emit engine-style events to subscribers, and tests can inject
// errors, drop the connection, script image pulls, and feed container logs
// and stats samples.
//
// A Service is safe for concurrent use.
package dockerfake

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/distribution/reference"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client/pkg/versions"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
)

// Method names a docker.Service method, for error injection and call
// records.
type Method string

// Methods of docker.Service.
const (
	MethodPing               Method = "Ping"
	MethodContainerList      Method = "ContainerList"
	MethodContainerInspect   Method = "ContainerInspect"
	MethodContainerStart     Method = "ContainerStart"
	MethodContainerStop      Method = "ContainerStop"
	MethodContainerRestart   Method = "ContainerRestart"
	MethodContainerPause     Method = "ContainerPause"
	MethodContainerUnpause   Method = "ContainerUnpause"
	MethodContainerRemove    Method = "ContainerRemove"
	MethodContainerLogs      Method = "ContainerLogs"
	MethodContainerStats     Method = "ContainerStats"
	MethodImageList          Method = "ImageList"
	MethodImageInspect       Method = "ImageInspect"
	MethodImagePull          Method = "ImagePull"
	MethodImageRemove        Method = "ImageRemove"
	MethodImagePruneDangling Method = "ImagePruneDangling"
	MethodVolumeList         Method = "VolumeList"
	MethodVolumeInspect      Method = "VolumeInspect"
	MethodVolumeCreate       Method = "VolumeCreate"
	MethodVolumeRemove       Method = "VolumeRemove"
	MethodExecCreate         Method = "ExecCreate"
	MethodExecStart          Method = "ExecStart"
	MethodExecInspect        Method = "ExecInspect"
	MethodEvents             Method = "Events"
)

// Call records one call to the fake. Args holds the arguments after the
// context, in order, with their declared types (for example a
// docker.ContainerRemoveOptions value).
type Call struct {
	Method Method
	Args   []any
}

// PullScript describes how a scripted pull behaves.
type PullScript struct {
	// Messages are the progress messages yielded, in order.
	Messages []jsonstream.Message
	// Gate, if non-nil, holds the pull after Messages until it receives a
	// value or is closed, so tests can observe an in-progress pull.
	Gate <-chan struct{}
	// Err, if non-nil, fails the pull: after Messages it yields a final
	// message whose Error carries Err's text, as a registry error would, and
	// no image is added.
	Err error
	// Image is added (tagged with the pulled reference) when the pull
	// completes successfully. A zero ID gets a generated one.
	Image image.Summary
}

// DefaultHost is the endpoint a new fake reports.
const DefaultHost = "unix:///var/run/docker.sock"

// clientAPIVersion is the API version the fake client starts with, like the
// SDK's maximum version.
const clientAPIVersion = "1.57"

// AnonymousVolumeLabel marks volumes the engine created for a container
// without a name; ContainerRemove with RemoveVolumes removes them.
const AnonymousVolumeLabel = "com.docker.volume.anonymous"

// Labels Docker Compose sets on the containers it creates. They match the
// constants of the same purpose in internal/store, which this package
// cannot import.
const (
	ComposeProjectLabel    = "com.docker.compose.project"
	ComposeServiceLabel    = "com.docker.compose.service"
	ComposeOneoffLabel     = "com.docker.compose.oneoff"
	ComposeWorkingDirLabel = "com.docker.compose.project.working_dir"
)

// Compose describes the Compose labels of a container, for AddContainer:
//
//	f.AddContainer(container.Summary{
//		Names:  []string{"/myapp-web-1"},
//		State:  container.StateRunning,
//		Labels: dockerfake.Compose{Project: "myapp", Service: "web"}.Labels(),
//	})
type Compose struct {
	Project    string
	Service    string
	WorkingDir string // omitted from the labels when empty
	OneOff     bool   // created by `docker compose run`
}

// Labels returns the labels Compose would set, as a new map. Like Compose,
// it always sets the one-off label, to "True" or "False".
func (c Compose) Labels() map[string]string {
	l := map[string]string{
		ComposeProjectLabel: c.Project,
		ComposeServiceLabel: c.Service,
		ComposeOneoffLabel:  "False",
	}
	if c.OneOff {
		l[ComposeOneoffLabel] = "True"
	}
	if c.WorkingDir != "" {
		l[ComposeWorkingDirLabel] = c.WorkingDir
	}
	return l
}

// Service is an in-memory docker.Service. The zero value is not usable;
// create one with New.
type Service struct {
	mu sync.Mutex

	endpoint      dockerctx.Endpoint
	host          string
	apiVersion    string
	engineVersion string
	connected     bool
	connErr       error

	containers []container.Summary // insertion order
	tty        map[string]bool
	images     []image.Summary
	volumes    []volume.Volume
	logs       map[string]*logBuffer
	shells     map[string]map[string]bool
	execs      map[string]*execState
	pulls      map[string]PullScript
	stats      map[string][]container.StatsResponse // queued samples by container ID
	statsOpen  map[*statsStream]struct{}            // streams not yet closed

	errs     map[Method]error
	errsOnce map[Method][]error
	errsFor  map[failKey]error
	calls    []Call
	subs     map[*subscription]struct{}
	nextID   uint64
}

var _ docker.Service = (*Service)(nil)

type execState struct {
	containerID string
	cmd         []string
	started     bool
	exitCode    int
	pid         int
}

// New returns an empty, connected fake.
func New() *Service {
	return &Service{
		endpoint:      dockerctx.DefaultEndpoint(),
		host:          DefaultHost,
		apiVersion:    clientAPIVersion,
		engineVersion: clientAPIVersion,
		connected:     true,
		tty:           map[string]bool{},
		logs:          map[string]*logBuffer{},
		shells:        map[string]map[string]bool{},
		execs:         map[string]*execState{},
		pulls:         map[string]PullScript{},
		stats:         map[string][]container.StatsResponse{},
		statsOpen:     map[*statsStream]struct{}{},
		errs:          map[Method]error{},
		errsOnce:      map[Method][]error{},
		errsFor:       map[failKey]error{},
		subs:          map[*subscription]struct{}{},
	}
}

// ---------------------------------------------------------------------------
// Test controls

// SetEndpoint sets the resolved context reported by Endpoint. It does not
// change Host; set both to model a named context.
func (s *Service) SetEndpoint(ep dockerctx.Endpoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.endpoint = ep
}

// SetHost sets the endpoint reported by Host.
func (s *Service) SetHost(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host = host
}

// SetEngineAPIVersion sets the API version the fake engine supports. Ping
// negotiates the client version down to it.
func (s *Service) SetEngineAPIVersion(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engineVersion = v
}

// SetError makes every call to m fail with err until it is cleared with a
// nil err. Calls are still recorded.
func (s *Service) SetError(m Method, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.errs, m)
		return
	}
	s.errs[m] = err
}

// FailNext makes the next call to m fail with err. Several calls queue up
// and take precedence over SetError.
func (s *Service) FailNext(m Method, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errsOnce[m] = append(s.errsOnce[m], err)
}

// FailFor makes every call to m whose target is id fail with err, until it
// is cleared with a nil err; calls to m for other targets are unaffected.
// Unlike SetError and FailNext, it can pick one container among calls made
// in parallel. The target is the call's first argument (the container ID
// for container methods) compared exactly as passed, so a container must
// be referred to the same way, usually by its full ID. Calls are still
// recorded. A cancelled context, FailNext and a lost connection take
// precedence over FailFor, and FailFor takes precedence over SetError.
func (s *Service) FailFor(m Method, id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := failKey{method: m, target: id}
	if err == nil {
		delete(s.errsFor, k)
		return
	}
	s.errsFor[k] = err
}

// failKey selects the calls FailFor applies to.
type failKey struct {
	method Method
	target string
}

// Disconnect simulates losing the engine: every open event and stats stream
// ends with err (a generic connection error if err is nil), and every call
// fails until Reconnect.
func (s *Service) Disconnect(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		err = fmt.Errorf("Cannot connect to the Docker daemon at %s. Is the docker daemon running?", s.host) //nolint:staticcheck // ST1005: verbatim Docker client text, classified by internal/docker/errors.go
	}
	s.connected = false
	s.connErr = err
	for sub := range s.subs {
		sub.fail(err)
		delete(s.subs, sub)
	}
	for st := range s.statsOpen {
		st.endLocked(err)
	}
}

// Reconnect undoes Disconnect.
func (s *Service) Reconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = true
	s.connErr = nil
}

// DropEvents ends every open event stream with err (io.ErrUnexpectedEOF if
// nil) without disconnecting the fake.
func (s *Service) DropEvents(err error) {
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for sub := range s.subs {
		sub.fail(err)
		delete(s.subs, sub)
	}
}

// Subscribers returns the number of open event streams.
func (s *Service) Subscribers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

// Emit delivers msg to every open event stream, as if an external client
// had changed something without the fake's state changing.
func (s *Service) Emit(msg events.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emitLocked(msg)
}

// Calls returns a copy of every call recorded so far.
func (s *Service) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

// CallsTo returns the recorded calls to m.
func (s *Service) CallsTo(m Method) []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Call
	for _, c := range s.calls {
		if c.Method == m {
			out = append(out, c)
		}
	}
	return out
}

// ResetCalls forgets the recorded calls.
func (s *Service) ResetCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

// AddContainer adds c, or replaces the container with the same ID, and
// emits a container event ("create", or "update" for a replacement). A
// zero ID gets a generated one, which is returned. Like the engine, names
// in c.Names should carry a leading "/". Replacing a container with one that
// is not running ends its stats streams. To model a container created by
// Docker Compose, set c.Labels from Compose.Labels.
func (s *Service) AddContainer(c container.Summary) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == "" {
		c.ID = s.newIDLocked()
	}
	c = cloneContainer(c)
	action := events.ActionCreate
	if i := s.containerIndexLocked(c.ID); i >= 0 {
		s.containers[i] = c
		action = events.ActionUpdate
		if c.State != container.StateRunning {
			s.endStatsLocked(c.ID)
		}
	} else {
		s.containers = append(s.containers, c)
	}
	s.emitLocked(containerEvent(action, c))
	return c.ID
}

// RemoveContainer removes the container with the given ID as an external
// client would, and emits a "destroy" event. It reports whether the
// container existed.
func (s *Service) RemoveContainer(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.containerIndexLocked(id)
	if i < 0 {
		return false
	}
	c := s.containers[i]
	s.containers = slices.Delete(s.containers, i, i+1)
	s.endLogsLocked(id)
	s.endStatsLocked(id)
	s.emitLocked(containerEvent(events.ActionDestroy, c))
	return true
}

// SetTTY records whether the container was created with a TTY, which
// ContainerInspect reports in Config.Tty.
func (s *Service) SetTTY(containerID string, tty bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tty[containerID] = tty
}

// SetShells sets the executables that exist in the container. ExecStart
// fails for commands whose executable is not among them.
func (s *Service) SetShells(containerID string, paths ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	s.shells[containerID] = set
}

// WriteLog appends one log entry (typically a line ending in "\n") to the
// container's stdout, or to stderr if stderr is set. ContainerLogs encodes
// entries as the engine does: stdcopy frames for containers without a TTY
// (see SetTTY) and raw bytes for containers with one. Readers that follow
// the log receive the entry immediately.
func (s *Service) WriteLog(containerID string, stderr bool, payload string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logBufferLocked(containerID).write(logEntry{stderr: stderr, payload: []byte(payload)})
}

// SetStats queues samples, in order, for the container's stats stream.
// containerID may be anything ContainerStats accepts; samples for an ID
// that matches no container are kept under that ID. Queued samples are
// consumed: each is yielded once, by whichever open stream of the container
// reads it first, and samples queued while no stream is open wait for the
// next one. Stopping or removing the container hands its queue to the
// streams that end, so a stream opened after a restart starts empty.
func (s *Service) SetStats(containerID string, samples ...container.StatsResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, err := s.findContainerLocked(containerID); err == nil {
		containerID = c.ID
	}
	for _, sample := range samples {
		s.stats[containerID] = append(s.stats[containerID], cloneStats(sample))
	}
	for st := range s.statsOpen {
		if st.containerID == containerID {
			st.signal()
		}
	}
}

// StatsStreams returns the number of stats streams opened by ContainerStats
// and not yet closed. A stream is closed by its Close, when its Samples
// iteration ends, or when the context passed to ContainerStats is
// cancelled; a stream the fake ended (see Disconnect) still counts until
// then.
func (s *Service) StatsStreams() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.statsOpen)
}

// AddImage adds img, or replaces the image with the same ID, and emits an
// image event. A zero ID gets a generated one, which is returned. Tags in
// RepoTags should be in familiar form ("nginx:1.27").
func (s *Service) AddImage(img image.Summary) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if img.ID == "" {
		img.ID = "sha256:" + s.newIDLocked()
	}
	img = cloneImage(img)
	action := events.ActionPull
	if i := s.imageIndexLocked(img.ID); i >= 0 {
		s.images[i] = img
		action = events.ActionTag
	} else {
		s.images = append(s.images, img)
	}
	s.emitLocked(imageEvent(action, img.ID, firstTag(img)))
	return img.ID
}

// RemoveImage removes the image with the given ID as an external client
// would, and emits a "delete" event. It reports whether the image existed.
func (s *Service) RemoveImage(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.imageIndexLocked(id)
	if i < 0 {
		return false
	}
	s.images = slices.Delete(s.images, i, i+1)
	s.emitLocked(imageEvent(events.ActionDelete, id, ""))
	return true
}

// AddVolume adds v, or replaces the volume with the same name, and emits a
// "create" volume event. Empty Driver, Mountpoint and Scope get the local
// driver's values.
func (s *Service) AddVolume(v volume.Volume) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v = s.completeVolume(v)
	if i := s.volumeIndexLocked(v.Name); i >= 0 {
		s.volumes[i] = v
	} else {
		s.volumes = append(s.volumes, v)
	}
	s.emitLocked(volumeEvent(events.ActionCreate, v.Name))
}

// RemoveVolume removes the named volume as an external client would, and
// emits a "destroy" event. It reports whether the volume existed.
func (s *Service) RemoveVolume(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.volumeIndexLocked(name)
	if i < 0 {
		return false
	}
	s.volumes = slices.Delete(s.volumes, i, i+1)
	s.emitLocked(volumeEvent(events.ActionDestroy, name))
	return true
}

// SetPullScript scripts the pull of ref. ref is normalized, so "redis" and
// "redis:latest" are the same script. Pulling an unscripted reference fails
// like a pull of a repository that does not exist.
func (s *Service) SetPullScript(ref string, script PullScript) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pulls[normalizeRef(ref)] = script
}

// ---------------------------------------------------------------------------
// docker.Service: connection

// Endpoint implements docker.Service. A new fake reports the default
// context.
func (s *Service) Endpoint() dockerctx.Endpoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.endpoint
}

// Host implements docker.Service.
func (s *Service) Host() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host
}

// APIVersion implements docker.Service.
func (s *Service) APIVersion() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apiVersion
}

// Close implements docker.Service. It ends every open event and stats
// stream.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := errors.New("client closed")
	for sub := range s.subs {
		sub.fail(err)
		delete(s.subs, sub)
	}
	for st := range s.statsOpen {
		st.endLocked(err)
	}
	return nil
}

// Ping implements docker.Service.
func (s *Service) Ping(ctx context.Context) (docker.PingResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodPing); err != nil {
		return docker.PingResult{}, err
	}
	if versions.LessThan(s.engineVersion, s.apiVersion) {
		s.apiVersion = s.engineVersion
	}
	return docker.PingResult{APIVersion: s.engineVersion, OSType: "linux"}, nil
}

// ---------------------------------------------------------------------------
// docker.Service: containers

// ContainerList implements docker.Service.
func (s *Service) ContainerList(ctx context.Context, opts docker.ContainerListOptions) ([]container.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerList, opts); err != nil {
		return nil, err
	}
	out := make([]container.Summary, 0, len(s.containers))
	for _, c := range s.containers {
		if opts.All || c.State == container.StateRunning {
			out = append(out, cloneContainer(c))
		}
	}
	return out, nil
}

// ContainerInspect implements docker.Service.
func (s *Service) ContainerInspect(ctx context.Context, id string) (docker.ContainerInspectResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerInspect, id); err != nil {
		return docker.ContainerInspectResult{}, err
	}
	c, err := s.findContainerLocked(id)
	if err != nil {
		return docker.ContainerInspectResult{}, err
	}
	name := ""
	if len(c.Names) > 0 {
		name = c.Names[0]
	}
	resp := container.InspectResponse{
		ID:      c.ID,
		Name:    name,
		Image:   c.ImageID,
		Created: time.Unix(c.Created, 0).UTC().Format(time.RFC3339Nano),
		State: &container.State{
			Status: c.State,
			// Like the engine, a paused or restarting container still counts
			// as running.
			Running:    isActive(c.State),
			Paused:     c.State == container.StatePaused,
			Restarting: c.State == container.StateRestarting,
		},
		Config: &container.Config{
			Image:  c.Image,
			Labels: maps.Clone(c.Labels),
			Tty:    s.tty[c.ID],
		},
		Mounts: slices.Clone(c.Mounts),
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return docker.ContainerInspectResult{}, err
	}
	return docker.ContainerInspectResult{Container: resp, Raw: raw}, nil
}

// ContainerStart implements docker.Service.
func (s *Service) ContainerStart(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerStart, id); err != nil {
		return err
	}
	c, err := s.findContainerLocked(id)
	if err != nil {
		return err
	}
	switch c.State {
	case container.StateRunning:
		return nil // the engine answers 304 Not Modified
	case container.StatePaused:
		return cerrdefs.ErrConflict.WithMessage("cannot start a paused container, try unpause instead")
	case container.StateRemoving, container.StateDead:
		return cerrdefs.ErrConflict.WithMessage(fmt.Sprintf("container %s is marked for removal and cannot be started", c.ID))
	}
	c.State = container.StateRunning
	c.Status = "Up Less than a second"
	s.logBufferLocked(c.ID).restart()
	s.emitLocked(containerEvent(events.ActionStart, *c))
	return nil
}

// ContainerStop implements docker.Service.
func (s *Service) ContainerStop(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerStop, id); err != nil {
		return err
	}
	c, err := s.findContainerLocked(id)
	if err != nil {
		return err
	}
	if !isActive(c.State) {
		return nil // the engine answers 304 Not Modified
	}
	s.stopLocked(c)
	return nil
}

// ContainerRestart implements docker.Service.
func (s *Service) ContainerRestart(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerRestart, id); err != nil {
		return err
	}
	c, err := s.findContainerLocked(id)
	if err != nil {
		return err
	}
	if isActive(c.State) {
		s.stopLocked(c)
	}
	c.State = container.StateRunning
	c.Status = "Up Less than a second"
	s.logBufferLocked(c.ID).restart()
	s.emitLocked(containerEvent(events.ActionStart, *c))
	s.emitLocked(containerEvent(events.ActionRestart, *c))
	return nil
}

// ContainerPause implements docker.Service. Like the engine, it refuses
// (conflict) a container that is not running, already paused, or restarting.
// Open stats streams are left alone.
func (s *Service) ContainerPause(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerPause, id); err != nil {
		return err
	}
	c, err := s.findContainerLocked(id)
	if err != nil {
		return err
	}
	switch c.State {
	case container.StateRunning:
	case container.StatePaused:
		return cerrdefs.ErrConflict.WithMessage(fmt.Sprintf("container %s is already paused", c.ID))
	case container.StateRestarting:
		return cerrdefs.ErrConflict.WithMessage(fmt.Sprintf("Container %s is restarting, wait until the container is running", c.ID))
	default:
		return cerrdefs.ErrConflict.WithMessage(fmt.Sprintf("container %s is not running", c.ID))
	}
	c.State = container.StatePaused
	c.Status = strings.TrimSuffix(c.Status, pausedStatusSuffix) + pausedStatusSuffix
	s.emitLocked(containerEvent(events.ActionPause, *c))
	return nil
}

// ContainerUnpause implements docker.Service. Like the engine, it refuses a
// container that is not paused. The engine (verified on 29.10) answers that
// refusal with a 500, not a 409, so the error is internal, not a conflict.
func (s *Service) ContainerUnpause(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerUnpause, id); err != nil {
		return err
	}
	c, err := s.findContainerLocked(id)
	if err != nil {
		return err
	}
	if c.State != container.StatePaused {
		return cerrdefs.ErrInternal.WithMessage(fmt.Sprintf("Container %s is not paused", c.ID))
	}
	c.State = container.StateRunning
	c.Status = strings.TrimSuffix(c.Status, pausedStatusSuffix)
	s.emitLocked(containerEvent(events.ActionUnPause, *c))
	return nil
}

// pausedStatusSuffix ends the engine's status text of a paused container,
// as in "Up 5 minutes (Paused)".
const pausedStatusSuffix = " (Paused)"

// ContainerRemove implements docker.Service.
func (s *Service) ContainerRemove(ctx context.Context, id string, opts docker.ContainerRemoveOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerRemove, id, opts); err != nil {
		return err
	}
	c, err := s.findContainerLocked(id)
	if err != nil {
		return err
	}
	if isActive(c.State) {
		if !opts.Force {
			return cerrdefs.ErrConflict.WithMessage(fmt.Sprintf(
				"cannot remove container %q: container is %s: stop the container before removing or force remove",
				primaryName(*c), c.State))
		}
		s.stopLocked(c)
	}
	removed := *c
	s.containers = slices.DeleteFunc(s.containers, func(x container.Summary) bool { return x.ID == removed.ID })
	s.endLogsLocked(removed.ID)
	s.endStatsLocked(removed.ID)
	s.emitLocked(containerEvent(events.ActionDestroy, removed))
	if opts.RemoveVolumes {
		for _, m := range removed.Mounts {
			if m.Type != mount.TypeVolume || s.volumeInUseLocked(m.Name) {
				continue
			}
			if i := s.volumeIndexLocked(m.Name); i >= 0 {
				if _, anon := s.volumes[i].Labels[AnonymousVolumeLabel]; anon {
					s.volumes = slices.Delete(s.volumes, i, i+1)
					s.emitLocked(volumeEvent(events.ActionDestroy, m.Name))
				}
			}
		}
	}
	return nil
}

// ContainerLogs implements docker.Service. Tail counts whole log entries.
func (s *Service) ContainerLogs(ctx context.Context, id string, opts docker.LogsOptions) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerLogs, id, opts); err != nil {
		return nil, err
	}
	c, err := s.findContainerLocked(id)
	if err != nil {
		return nil, err
	}
	stdout, stderr := opts.Stdout, opts.Stderr
	if !stdout && !stderr {
		stdout, stderr = true, true
	}
	return s.logBufferLocked(c.ID).reader(ctx, logReaderOptions{
		tty:    s.tty[c.ID],
		stdout: stdout,
		stderr: stderr,
		tail:   opts.Tail,
		follow: opts.Follow && isActive(c.State),
	}), nil
}

// ContainerStats implements docker.Service. The stream yields the samples
// queued with SetStats and waits for more. Like the engine, it ends without
// an error when the container stops or is removed, after delivering the
// samples queued before that, and fails for unknown and non-running
// containers (paused and restarting included). A lost connection (see
// Disconnect) ends it at once with an error.
func (s *Service) ContainerStats(ctx context.Context, id string) (docker.StatsStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodContainerStats, id); err != nil {
		return nil, err
	}
	c, err := s.findContainerLocked(id)
	if err != nil {
		return nil, err
	}
	if c.State != container.StateRunning {
		return nil, cerrdefs.ErrConflict.WithMessage(fmt.Sprintf("container %s is not running", c.ID))
	}
	st := &statsStream{
		svc:         s,
		containerID: c.ID,
		openCtx:     ctx,
		wake:        make(chan struct{}, 1),
		closed:      make(chan struct{}),
	}
	s.statsOpen[st] = struct{}{}
	// Like the SDK, which ties the HTTP request to ctx, cancelling it closes
	// the stream even if nobody iterates it. AfterFunc runs Close on its own
	// goroutine, so it never runs under s.mu.
	st.stopAfter = context.AfterFunc(ctx, st.release)
	return st, nil
}

// ---------------------------------------------------------------------------
// docker.Service: images

// ImageList implements docker.Service.
func (s *Service) ImageList(ctx context.Context) ([]image.Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodImageList); err != nil {
		return nil, err
	}
	out := make([]image.Summary, 0, len(s.images))
	for _, img := range s.images {
		img = cloneImage(img)
		img.Containers = int64(s.imageUsersLocked(img.ID))
		out = append(out, img)
	}
	return out, nil
}

// ImageInspect implements docker.Service.
func (s *Service) ImageInspect(ctx context.Context, ref string) (docker.ImageInspectResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodImageInspect, ref); err != nil {
		return docker.ImageInspectResult{}, err
	}
	if err := docker.ValidateImageRef(ref); err != nil {
		return docker.ImageInspectResult{}, err
	}
	i, _, err := s.resolveImageLocked(ref)
	if err != nil {
		return docker.ImageInspectResult{}, err
	}
	img := s.images[i]
	resp := image.InspectResponse{
		ID:          img.ID,
		RepoTags:    slices.Clone(img.RepoTags),
		RepoDigests: slices.Clone(img.RepoDigests),
		Created:     time.Unix(img.Created, 0).UTC().Format(time.RFC3339Nano),
		Size:        img.Size,
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return docker.ImageInspectResult{}, err
	}
	return docker.ImageInspectResult{Image: resp, Raw: raw}, nil
}

// ImagePull implements docker.Service.
func (s *Service) ImagePull(ctx context.Context, ref string) (docker.PullStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodImagePull, ref); err != nil {
		return nil, err
	}
	if err := docker.ValidateImageRef(ref); err != nil {
		return nil, err
	}
	norm := normalizeRef(ref)
	script, ok := s.pulls[norm]
	if !ok {
		return nil, cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf(
			"pull access denied for %s, repository does not exist or may require 'docker login'", repoName(norm)))
	}
	return &pullStream{svc: s, ref: norm, script: script, pullCtx: ctx, done: make(chan struct{})}, nil
}

// ImageRemove implements docker.Service. PruneChildren is recorded but has
// no effect, because the fake does not model parent images.
func (s *Service) ImageRemove(ctx context.Context, ref string, opts docker.ImageRemoveOptions) ([]image.DeleteResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodImageRemove, ref, opts); err != nil {
		return nil, err
	}
	if err := docker.ValidateImageRef(ref); err != nil {
		return nil, err
	}
	i, tag, err := s.resolveImageLocked(ref)
	if err != nil {
		return nil, err
	}
	img := &s.images[i]
	users := s.imageUsersLocked(img.ID)

	if tag != "" && len(img.RepoTags) > 1 {
		// Another tag keeps the image alive: only untag, even if in use.
		img.RepoTags = slices.DeleteFunc(img.RepoTags, func(t string) bool { return t == tag })
		s.emitLocked(imageEvent(events.ActionUnTag, img.ID, tag))
		return []image.DeleteResponse{{Untagged: tag}}, nil
	}
	if !opts.Force {
		if tag == "" && len(img.RepoTags) > 1 {
			return nil, cerrdefs.ErrConflict.WithMessage(fmt.Sprintf(
				"conflict: unable to delete %s (must be forced) - image is referenced in multiple repositories", shortID(img.ID)))
		}
		if users > 0 {
			return nil, cerrdefs.ErrConflict.WithMessage(fmt.Sprintf(
				"conflict: unable to remove repository reference %q (must force) - container %s is using its referenced image %s",
				ref, shortID(s.firstImageUserLocked(img.ID)), shortID(img.ID)))
		}
	}
	var resp []image.DeleteResponse
	for _, t := range img.RepoTags {
		resp = append(resp, image.DeleteResponse{Untagged: t})
	}
	id := img.ID
	resp = append(resp, image.DeleteResponse{Deleted: id})
	s.images = slices.Delete(s.images, i, i+1)
	s.emitLocked(imageEvent(events.ActionDelete, id, ""))
	return resp, nil
}

// ImagePruneDangling implements docker.Service.
func (s *Service) ImagePruneDangling(ctx context.Context) (image.PruneReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodImagePruneDangling); err != nil {
		return image.PruneReport{}, err
	}
	var report image.PruneReport
	kept := s.images[:0:0]
	for _, img := range s.images {
		if isDangling(img) && s.imageUsersLocked(img.ID) == 0 {
			report.ImagesDeleted = append(report.ImagesDeleted, image.DeleteResponse{Deleted: img.ID})
			if img.Size > 0 {
				report.SpaceReclaimed += uint64(img.Size)
			}
			continue
		}
		kept = append(kept, img)
	}
	s.images = kept
	for _, d := range report.ImagesDeleted {
		s.emitLocked(imageEvent(events.ActionDelete, d.Deleted, ""))
	}
	return report, nil
}

// ---------------------------------------------------------------------------
// docker.Service: volumes

// VolumeList implements docker.Service.
func (s *Service) VolumeList(ctx context.Context) ([]volume.Volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodVolumeList); err != nil {
		return nil, err
	}
	out := make([]volume.Volume, 0, len(s.volumes))
	for _, v := range s.volumes {
		out = append(out, cloneVolume(v))
	}
	return out, nil
}

// VolumeInspect implements docker.Service.
func (s *Service) VolumeInspect(ctx context.Context, name string) (docker.VolumeInspectResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodVolumeInspect, name); err != nil {
		return docker.VolumeInspectResult{}, err
	}
	if err := docker.ValidateVolumeName(name); err != nil {
		return docker.VolumeInspectResult{}, err
	}
	i := s.volumeIndexLocked(name)
	if i < 0 {
		return docker.VolumeInspectResult{}, noSuchVolume(name)
	}
	v := cloneVolume(s.volumes[i])
	raw, err := json.Marshal(v)
	if err != nil {
		return docker.VolumeInspectResult{}, err
	}
	return docker.VolumeInspectResult{Volume: v, Raw: raw}, nil
}

// VolumeCreate implements docker.Service. Like the engine, creating an
// existing name returns the existing volume unchanged.
func (s *Service) VolumeCreate(ctx context.Context, name string) (volume.Volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodVolumeCreate, name); err != nil {
		return volume.Volume{}, err
	}
	if name == "" {
		name = s.newIDLocked()
	} else if err := docker.ValidateVolumeName(name); err != nil {
		return volume.Volume{}, err
	}
	if i := s.volumeIndexLocked(name); i >= 0 {
		return cloneVolume(s.volumes[i]), nil
	}
	v := s.completeVolume(volume.Volume{Name: name})
	s.volumes = append(s.volumes, v)
	s.emitLocked(volumeEvent(events.ActionCreate, name))
	return cloneVolume(v), nil
}

// VolumeRemove implements docker.Service. Like the engine, an in-use volume
// is never removed, even with Force.
func (s *Service) VolumeRemove(ctx context.Context, name string, opts docker.VolumeRemoveOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodVolumeRemove, name, opts); err != nil {
		return err
	}
	if err := docker.ValidateVolumeName(name); err != nil {
		return err
	}
	i := s.volumeIndexLocked(name)
	if i < 0 {
		if opts.Force {
			return nil
		}
		return noSuchVolume(name)
	}
	if s.volumeInUseLocked(name) {
		return cerrdefs.ErrConflict.WithMessage(fmt.Sprintf(
			"remove %s: volume is in use - [%s]", name, strings.Join(s.volumeUsersLocked(name), ", ")))
	}
	s.volumes = slices.Delete(s.volumes, i, i+1)
	s.emitLocked(volumeEvent(events.ActionDestroy, name))
	return nil
}

// ---------------------------------------------------------------------------
// docker.Service: exec

// ExecCreate implements docker.Service.
func (s *Service) ExecCreate(ctx context.Context, containerID string, cmd []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodExecCreate, containerID, slices.Clone(cmd)); err != nil {
		return "", err
	}
	if len(cmd) == 0 {
		return "", errors.New("exec: empty command")
	}
	c, err := s.findContainerLocked(containerID)
	if err != nil {
		return "", err
	}
	if c.State != container.StateRunning {
		return "", cerrdefs.ErrConflict.WithMessage(fmt.Sprintf("container %s is not running", c.ID))
	}
	id := s.newIDLocked()
	s.execs[id] = &execState{containerID: c.ID, cmd: slices.Clone(cmd)}
	return id, nil
}

// ExecStart implements docker.Service. The command finishes immediately:
// it succeeds with exit code 0 when its executable was registered with
// SetShells, and otherwise fails like the OCI runtime does.
func (s *Service) ExecStart(ctx context.Context, execID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodExecStart, execID); err != nil {
		return err
	}
	e, ok := s.execs[execID]
	if !ok {
		return cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("No such exec instance: %s", execID))
	}
	if e.started {
		return cerrdefs.ErrConflict.WithMessage(fmt.Sprintf("exec %s has already been started", execID))
	}
	if !s.shells[e.containerID][e.cmd[0]] {
		e.exitCode = 127
		return fmt.Errorf("OCI runtime exec failed: exec failed: unable to start container process: "+
			"exec: %q: stat %s: no such file or directory: unknown", e.cmd[0], e.cmd[0])
	}
	e.started = true
	e.pid = 1000 + len(s.execs)
	e.exitCode = 0
	return nil
}

// ExecInspect implements docker.Service.
func (s *Service) ExecInspect(ctx context.Context, execID string) (docker.ExecInspectResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.beginLocked(ctx, MethodExecInspect, execID); err != nil {
		return docker.ExecInspectResult{}, err
	}
	e, ok := s.execs[execID]
	if !ok {
		return docker.ExecInspectResult{}, cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("No such exec instance: %s", execID))
	}
	return docker.ExecInspectResult{Running: false, ExitCode: e.exitCode, Pid: e.pid}, nil
}

// ---------------------------------------------------------------------------
// docker.Service: events

// Events implements docker.Service. Events are delivered in order and never
// dropped; the stream ends with ctx's error when ctx is cancelled, or with
// the error passed to Disconnect or DropEvents.
func (s *Service) Events(ctx context.Context) docker.EventStream {
	msgs := make(chan events.Message)
	errc := make(chan error, 1)

	s.mu.Lock()
	err := s.beginLocked(ctx, MethodEvents)
	var sub *subscription
	if err == nil {
		sub = newSubscription()
		s.subs[sub] = struct{}{}
	}
	s.mu.Unlock()

	if err != nil {
		errc <- err
		close(errc)
		return docker.EventStream{Messages: msgs, Err: errc}
	}
	go func() {
		defer close(errc)
		errc <- sub.pump(ctx, msgs)
		s.mu.Lock()
		delete(s.subs, sub)
		s.mu.Unlock()
	}()
	return docker.EventStream{Messages: msgs, Err: errc}
}

// ---------------------------------------------------------------------------
// internals (callers hold s.mu)

// beginLocked records the call and returns the error it must fail with, if
// any: a cancelled context, a lost connection, or an injected error.
func (s *Service) beginLocked(ctx context.Context, m Method, args ...any) error {
	s.calls = append(s.calls, Call{Method: m, Args: args})
	if err := ctx.Err(); err != nil {
		return err
	}
	if q := s.errsOnce[m]; len(q) > 0 {
		err := q[0]
		if len(q) == 1 {
			delete(s.errsOnce, m)
		} else {
			s.errsOnce[m] = q[1:]
		}
		return err
	}
	if !s.connected {
		return s.connErr
	}
	if len(args) > 0 {
		if target, ok := args[0].(string); ok {
			if err, ok := s.errsFor[failKey{method: m, target: target}]; ok {
				return err
			}
		}
	}
	return s.errs[m]
}

func (s *Service) newIDLocked() string {
	s.nextID++
	return fmt.Sprintf("%064x", s.nextID)
}

func (s *Service) emitLocked(msg events.Message) {
	if msg.TimeNano == 0 {
		now := time.Now()
		msg.Time, msg.TimeNano = now.Unix(), now.UnixNano()
	}
	msg.Scope = "local"
	for sub := range s.subs {
		sub.push(msg)
	}
}

func (s *Service) containerIndexLocked(id string) int {
	return slices.IndexFunc(s.containers, func(c container.Summary) bool { return c.ID == id })
}

// findContainerLocked resolves a full ID, a unique ID prefix, or a name.
func (s *Service) findContainerLocked(ref string) (*container.Summary, error) {
	if err := docker.ValidateContainerID(ref); err != nil {
		return nil, err
	}
	if i := s.containerIndexLocked(ref); i >= 0 {
		return &s.containers[i], nil
	}
	for i := range s.containers {
		if slices.Contains(s.containers[i].Names, "/"+ref) {
			return &s.containers[i], nil
		}
	}
	match := -1
	for i := range s.containers {
		if strings.HasPrefix(s.containers[i].ID, ref) {
			if match >= 0 {
				return nil, cerrdefs.ErrInvalidArgument.WithMessage(fmt.Sprintf("multiple IDs found with provided prefix: %s", ref))
			}
			match = i
		}
	}
	if match < 0 {
		return nil, cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("No such container: %s", ref))
	}
	return &s.containers[match], nil
}

// stopLocked moves an active container to exited and ends log followers.
func (s *Service) stopLocked(c *container.Summary) {
	c.State = container.StateExited
	c.Status = "Exited (0) Less than a second ago"
	s.endLogsLocked(c.ID)
	s.endStatsLocked(c.ID)
	s.emitLocked(containerEvent(events.ActionKill, *c))
	s.emitLocked(containerEvent(events.ActionDie, *c))
	s.emitLocked(containerEvent(events.ActionStop, *c))
}

func (s *Service) logBufferLocked(id string) *logBuffer {
	b, ok := s.logs[id]
	if !ok {
		b = newLogBuffer()
		s.logs[id] = b
	}
	return b
}

func (s *Service) endLogsLocked(id string) {
	if b, ok := s.logs[id]; ok {
		b.end()
	}
}

// endStatsLocked ends the container's open stats streams cleanly, as the
// engine does when the container stops: samples queued before the stop are
// still delivered to those streams, then they end. The queue is detached
// from the container, so a stream opened after a restart starts empty.
func (s *Service) endStatsLocked(id string) {
	tail := &statsTail{items: s.stats[id]}
	delete(s.stats, id)
	for st := range s.statsOpen {
		if st.containerID == id && !st.ended {
			st.tail = tail
			st.endLocked(nil)
		}
	}
}

func (s *Service) imageIndexLocked(id string) int {
	return slices.IndexFunc(s.images, func(img image.Summary) bool { return img.ID == id })
}

// resolveImageLocked finds the image for ref the way the engine does: as a
// tag first, then as an ID (with or without the "sha256:" prefix, or a
// unique prefix of it). tag is the matched tag in familiar form, or "" when
// ref matched an ID.
func (s *Service) resolveImageLocked(ref string) (idx int, tag string, err error) {
	if !strings.HasPrefix(ref, "sha256:") {
		if norm := normalizeRef(ref); norm != "" {
			for i, img := range s.images {
				if slices.Contains(img.RepoTags, norm) {
					return i, norm, nil
				}
			}
		}
	}
	bare := strings.TrimPrefix(ref, "sha256:")
	if !isHex(bare) {
		return -1, "", noSuchImage(ref)
	}
	match := -1
	for i, img := range s.images {
		if strings.HasPrefix(strings.TrimPrefix(img.ID, "sha256:"), bare) {
			if match >= 0 {
				return -1, "", cerrdefs.ErrInvalidArgument.WithMessage(fmt.Sprintf("ambiguous image ID prefix: %s", ref))
			}
			match = i
		}
	}
	if match < 0 {
		return -1, "", noSuchImage(ref)
	}
	return match, "", nil
}

func (s *Service) imageUsersLocked(imageID string) int {
	n := 0
	for _, c := range s.containers {
		if c.ImageID == imageID {
			n++
		}
	}
	return n
}

func (s *Service) firstImageUserLocked(imageID string) string {
	for _, c := range s.containers {
		if c.ImageID == imageID {
			return c.ID
		}
	}
	return ""
}

func (s *Service) volumeIndexLocked(name string) int {
	return slices.IndexFunc(s.volumes, func(v volume.Volume) bool { return v.Name == name })
}

func (s *Service) volumeUsersLocked(name string) []string {
	var ids []string
	for _, c := range s.containers {
		for _, m := range c.Mounts {
			if m.Type == mount.TypeVolume && m.Name == name {
				ids = append(ids, c.ID)
				break
			}
		}
	}
	return ids
}

func (s *Service) volumeInUseLocked(name string) bool { return len(s.volumeUsersLocked(name)) > 0 }

func (s *Service) completeVolume(v volume.Volume) volume.Volume {
	if v.Driver == "" {
		v.Driver = "local"
	}
	if v.Mountpoint == "" && v.Driver == "local" {
		v.Mountpoint = "/var/lib/docker/volumes/" + v.Name + "/_data"
	}
	if v.Scope == "" {
		v.Scope = "local"
	}
	if v.CreatedAt == "" {
		v.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if v.Labels == nil {
		v.Labels = map[string]string{}
	}
	if v.Options == nil {
		v.Options = map[string]string{}
	}
	return cloneVolume(v)
}

// finishPull tags the scripted image with ref, moving the tag off any other
// image, and emits a "pull" event.
func (s *Service) finishPull(ref string, img image.Summary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if img.ID == "" {
		img.ID = "sha256:" + s.newIDLocked()
	}
	if img.Created == 0 {
		img.Created = time.Now().Unix()
	}
	for i := range s.images {
		s.images[i].RepoTags = slices.DeleteFunc(s.images[i].RepoTags, func(t string) bool { return t == ref })
	}
	if i := s.imageIndexLocked(img.ID); i >= 0 {
		if !slices.Contains(s.images[i].RepoTags, ref) {
			s.images[i].RepoTags = append(s.images[i].RepoTags, ref)
		}
	} else {
		img = cloneImage(img)
		img.RepoTags = append(slices.DeleteFunc(img.RepoTags, func(t string) bool { return t == ref }), ref)
		s.images = append(s.images, img)
	}
	s.emitLocked(imageEvent(events.ActionPull, img.ID, ref))
}

// ---------------------------------------------------------------------------
// pull stream

type pullStream struct {
	svc    *Service
	ref    string
	script PullScript
	// pullCtx is the context passed to ImagePull. Like the SDK, which ties
	// the HTTP request to it, cancelling it aborts the pull.
	pullCtx context.Context

	once sync.Once
	done chan struct{} // closed by Close
}

func (p *pullStream) Close() error {
	p.once.Do(func() { close(p.done) })
	return nil
}

func (p *pullStream) Messages(ctx context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(yield func(jsonstream.Message, error) bool) {
		defer func() { _ = p.Close() }() // Close never fails
		aborted := func() error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-p.pullCtx.Done():
				return p.pullCtx.Err()
			case <-p.done:
				return errors.New("pull stream closed")
			default:
				return nil
			}
		}
		for _, m := range p.script.Messages {
			if err := aborted(); err != nil {
				yield(jsonstream.Message{}, err)
				return
			}
			if !yield(m, nil) {
				return
			}
		}
		if p.script.Gate != nil {
			select {
			case <-p.script.Gate:
			case <-ctx.Done():
				yield(jsonstream.Message{}, ctx.Err())
				return
			case <-p.pullCtx.Done():
				yield(jsonstream.Message{}, p.pullCtx.Err())
				return
			case <-p.done:
				yield(jsonstream.Message{}, errors.New("pull stream closed"))
				return
			}
		}
		if err := aborted(); err != nil {
			yield(jsonstream.Message{}, err)
			return
		}
		if p.script.Err != nil {
			yield(jsonstream.Message{Error: &jsonstream.Error{Message: p.script.Err.Error()}}, nil)
			return
		}
		p.svc.finishPull(p.ref, p.script.Image)
		yield(jsonstream.Message{Status: "Status: Downloaded newer image for " + p.ref}, nil)
	}
}

// ---------------------------------------------------------------------------
// stats streams

// statsTail holds the samples queued when a container stopped, shared by
// the streams that the stop ended. Guarded by Service.mu.
type statsTail struct {
	items []container.StatsResponse
}

type statsStream struct {
	svc         *Service
	containerID string
	// openCtx is the context passed to ContainerStats; cancelling it closes
	// the stream.
	openCtx   context.Context
	stopAfter func() bool

	wake chan struct{} // capacity 1: new samples or the stream ended

	// Guarded by svc.mu.
	ended  bool
	endErr error      // nil for a clean end (the container stopped)
	tail   *statsTail // samples still to deliver after a clean end

	once   sync.Once
	closed chan struct{} // closed by Close
}

// endLocked marks the stream as ended by the engine with err (nil for a
// clean end). The caller holds st.svc.mu.
func (st *statsStream) endLocked(err error) {
	if st.ended {
		return
	}
	st.ended, st.endErr = true, err
	st.signal()
}

func (st *statsStream) signal() {
	select {
	case st.wake <- struct{}{}:
	default:
	}
}

// Close implements docker.StatsStream.
func (st *statsStream) Close() error {
	st.release()
	st.stopAfter() // set before ContainerStats returned, so before any Close
	return nil
}

// release closes the stream once. It is also the AfterFunc callback, which
// may run before ContainerStats has stored stopAfter, so it must not use it.
func (st *statsStream) release() {
	st.once.Do(func() {
		close(st.closed)
		st.svc.mu.Lock()
		delete(st.svc.statsOpen, st)
		st.svc.mu.Unlock()
	})
}

// aborted reports why the consumer side has ended the stream, if it has.
func (st *statsStream) aborted(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-st.openCtx.Done():
		return st.openCtx.Err()
	case <-st.closed:
		return docker.ErrStatsStreamClosed
	default:
		return nil
	}
}

func (st *statsStream) Samples(ctx context.Context) iter.Seq2[container.StatsResponse, error] {
	return func(yield func(container.StatsResponse, error) bool) {
		defer func() { _ = st.Close() }() // Close never fails
		for {
			if err := st.aborted(ctx); err != nil {
				yield(container.StatsResponse{}, err)
				return
			}
			s := st.svc
			s.mu.Lock()
			ended, endErr := st.ended, st.endErr
			var (
				sample container.StatsResponse
				have   bool
			)
			switch {
			case !ended:
				if q := s.stats[st.containerID]; len(q) > 0 {
					sample, have = q[0], true
					q[0] = container.StatsResponse{} // release references held by the backing array
					if len(q) == 1 {
						delete(s.stats, st.containerID)
					} else {
						s.stats[st.containerID] = q[1:]
					}
				}
			case endErr == nil && st.tail != nil && len(st.tail.items) > 0:
				sample, have = st.tail.items[0], true
				st.tail.items[0] = container.StatsResponse{}
				st.tail.items = st.tail.items[1:]
			}
			s.mu.Unlock()

			switch {
			case have:
				if !yield(sample, nil) {
					return
				}
				continue
			case ended:
				if endErr != nil {
					yield(container.StatsResponse{}, endErr)
				}
				return
			}
			select {
			case <-st.wake:
			case <-ctx.Done():
			case <-st.openCtx.Done():
			case <-st.closed:
			}
		}
	}
}

// ---------------------------------------------------------------------------
// event subscriptions

// subscription is an unbounded, ordered event queue drained into an
// unbuffered channel by pump, so that emitting never blocks the fake.
type subscription struct {
	mu      sync.Mutex
	queue   []events.Message
	failErr error
	wake    chan struct{} // capacity 1
}

func newSubscription() *subscription {
	return &subscription{wake: make(chan struct{}, 1)}
}

func (sub *subscription) push(msg events.Message) {
	sub.mu.Lock()
	sub.queue = append(sub.queue, msg)
	sub.mu.Unlock()
	sub.signal()
}

func (sub *subscription) fail(err error) {
	sub.mu.Lock()
	if sub.failErr == nil {
		sub.failErr = err
	}
	sub.mu.Unlock()
	sub.signal()
}

func (sub *subscription) signal() {
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

// pump delivers queued events to out until the subscription fails or ctx
// is cancelled, and returns the reason. Events queued before a failure are
// delivered first, as an engine flushes them before the stream ends.
func (sub *subscription) pump(ctx context.Context, out chan<- events.Message) error {
	for {
		sub.mu.Lock()
		var (
			msg     events.Message
			have    bool
			failErr = sub.failErr
		)
		if len(sub.queue) > 0 {
			msg, have = sub.queue[0], true
			sub.queue = sub.queue[1:]
		}
		sub.mu.Unlock()

		if have {
			select {
			case out <- msg:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if failErr != nil {
			return failErr
		}
		select {
		case <-sub.wake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// ---------------------------------------------------------------------------
// log buffers

// logEntry is one log record, as the engine stores it.
type logEntry struct {
	stderr  bool
	payload []byte
}

// logBuffer holds a container's log entries. Followers block for new
// entries until the container stops.
type logBuffer struct {
	mu      sync.Mutex
	entries []logEntry
	ended   bool
	changed chan struct{} // closed and replaced on every change
}

func newLogBuffer() *logBuffer {
	return &logBuffer{changed: make(chan struct{})}
}

func (b *logBuffer) notifyLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *logBuffer) write(e logEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, e)
	b.notifyLocked()
}

func (b *logBuffer) end() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ended = true
	b.notifyLocked()
}

func (b *logBuffer) restart() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ended = false
}

// logReaderOptions configures a logReader.
type logReaderOptions struct {
	tty            bool // raw payloads instead of stdcopy frames
	stdout, stderr bool // streams to include
	tail           int  // last tail matching entries; <= 0 means all
	follow         bool
}

// reader returns a reader over the entries selected by opts. Like the
// engine, tail counts whole log entries of the selected streams.
func (b *logBuffer) reader(ctx context.Context, opts logReaderOptions) io.ReadCloser {
	r := &logReader{buf: b, ctx: ctx, opts: opts, closed: make(chan struct{})}
	if opts.tail > 0 {
		b.mu.Lock()
		n := 0
		r.next = len(b.entries)
		for i := len(b.entries) - 1; i >= 0 && n < opts.tail; i-- {
			if r.wants(b.entries[i]) {
				r.next = i
				n++
			}
		}
		b.mu.Unlock()
	}
	return r
}

type logReader struct {
	buf     *logBuffer
	ctx     context.Context
	opts    logReaderOptions
	next    int    // index of the next entry to encode
	pending []byte // encoded bytes not yet returned

	closeOnce sync.Once
	closed    chan struct{}
}

func (r *logReader) wants(e logEntry) bool {
	if e.stderr {
		return r.opts.stderr
	}
	return r.opts.stdout
}

// encode renders an entry as the engine sends it: the raw payload for a
// TTY container, otherwise an stdcopy frame (stream byte, three zero bytes,
// big-endian payload length, payload).
func (r *logReader) encode(e logEntry) []byte {
	if r.opts.tty {
		return slices.Clone(e.payload)
	}
	frame := make([]byte, 8, 8+len(e.payload))
	frame[0] = 1 // stdout
	if e.stderr {
		frame[0] = 2
	}
	binary.BigEndian.PutUint32(frame[4:], uint32(len(e.payload)))
	return append(frame, e.payload...)
}

func (r *logReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if len(r.pending) > 0 {
			n := copy(p, r.pending)
			r.pending = r.pending[n:]
			return n, nil
		}
		select {
		case <-r.closed:
			return 0, io.ErrClosedPipe
		default:
		}

		r.buf.mu.Lock()
		for r.next < len(r.buf.entries) && len(r.pending) == 0 {
			if e := r.buf.entries[r.next]; r.wants(e) {
				r.pending = r.encode(e)
			}
			r.next++
		}
		ended, changed := r.buf.ended, r.buf.changed
		r.buf.mu.Unlock()

		if len(r.pending) > 0 {
			continue
		}
		if !r.opts.follow || ended {
			return 0, io.EOF
		}
		select {
		case <-changed:
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		case <-r.closed:
			return 0, io.ErrClosedPipe
		}
	}
}

func (r *logReader) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

// ---------------------------------------------------------------------------
// helpers

func isActive(st container.ContainerState) bool {
	return st == container.StateRunning || st == container.StatePaused || st == container.StateRestarting
}

func isDangling(img image.Summary) bool {
	for _, t := range img.RepoTags {
		if t != "<none>:<none>" {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// normalizeRef returns ref in familiar "name:tag" form with a default
// "latest" tag, or "" if ref is not a tagged name (for example a digest).
func normalizeRef(ref string) string {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return ""
	}
	if _, ok := named.(reference.Digested); ok {
		return reference.FamiliarString(named)
	}
	return reference.FamiliarString(reference.TagNameOnly(named))
}

func repoName(ref string) string {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return ref
	}
	return reference.FamiliarName(named)
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func primaryName(c container.Summary) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return shortID(c.ID)
}

func firstTag(img image.Summary) string {
	if len(img.RepoTags) > 0 {
		return img.RepoTags[0]
	}
	return ""
}

func noSuchImage(ref string) error {
	return cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("No such image: %s", ref))
}

func noSuchVolume(name string) error {
	return cerrdefs.ErrNotFound.WithMessage(fmt.Sprintf("get %s: no such volume", name))
}

func containerEvent(action events.Action, c container.Summary) events.Message {
	attrs := map[string]string{"image": c.Image}
	if len(c.Names) > 0 {
		attrs["name"] = strings.TrimPrefix(c.Names[0], "/")
	}
	return events.Message{
		Type:   events.ContainerEventType,
		Action: action,
		Actor:  events.Actor{ID: c.ID, Attributes: attrs},
	}
}

func imageEvent(action events.Action, id, name string) events.Message {
	attrs := map[string]string{}
	if name != "" {
		attrs["name"] = name
	}
	return events.Message{
		Type:   events.ImageEventType,
		Action: action,
		Actor:  events.Actor{ID: id, Attributes: attrs},
	}
}

func volumeEvent(action events.Action, name string) events.Message {
	return events.Message{
		Type:   events.VolumeEventType,
		Action: action,
		Actor:  events.Actor{ID: name, Attributes: map[string]string{"driver": "local"}},
	}
}

func cloneContainer(c container.Summary) container.Summary {
	c.Names = slices.Clone(c.Names)
	c.Ports = slices.Clone(c.Ports)
	c.Labels = maps.Clone(c.Labels)
	c.Mounts = slices.Clone(c.Mounts)
	if c.Health != nil {
		h := *c.Health
		c.Health = &h
	}
	if c.NetworkSettings != nil {
		ns := *c.NetworkSettings
		ns.Networks = maps.Clone(ns.Networks)
		c.NetworkSettings = &ns
	}
	c.HostConfig.Annotations = maps.Clone(c.HostConfig.Annotations)
	return c
}

func cloneImage(img image.Summary) image.Summary {
	img.RepoTags = slices.Clone(img.RepoTags)
	img.RepoDigests = slices.Clone(img.RepoDigests)
	img.Labels = maps.Clone(img.Labels)
	img.Manifests = slices.Clone(img.Manifests)
	return img
}

func cloneStats(s container.StatsResponse) container.StatsResponse {
	s.CPUStats.CPUUsage.PercpuUsage = slices.Clone(s.CPUStats.CPUUsage.PercpuUsage)
	s.PreCPUStats.CPUUsage.PercpuUsage = slices.Clone(s.PreCPUStats.CPUUsage.PercpuUsage)
	s.MemoryStats.Stats = maps.Clone(s.MemoryStats.Stats)
	s.Networks = maps.Clone(s.Networks)
	b := &s.BlkioStats
	for _, p := range []*[]container.BlkioStatEntry{
		&b.IoServiceBytesRecursive, &b.IoServicedRecursive, &b.IoQueuedRecursive, &b.IoServiceTimeRecursive,
		&b.IoWaitTimeRecursive, &b.IoMergedRecursive, &b.IoTimeRecursive, &b.SectorsRecursive,
	} {
		*p = slices.Clone(*p)
	}
	return s
}

func cloneVolume(v volume.Volume) volume.Volume {
	v.Labels = maps.Clone(v.Labels)
	v.Options = maps.Clone(v.Options)
	v.Status = maps.Clone(v.Status)
	return v
}
