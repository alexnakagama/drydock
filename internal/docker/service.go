package docker

import (
	"context"
	"encoding/json"
	"io"
	"iter"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/volume"
)

// Service is drydock's narrow interface to the Docker engine.
//
// Data types come from the stable engine API module
// (github.com/moby/moby/api); option types are owned by this package so that
// the pre-v1 client module (github.com/moby/moby/client) never leaks to
// callers. Every method that takes a context honors its cancellation.
//
// Implementations must be safe for concurrent use.
type Service interface {
	// Host returns the engine endpoint the service talks to, for example
	// "unix:///var/run/docker.sock".
	Host() string
	// APIVersion returns the engine API version in use. Before the first
	// successful Ping it is the client's default (maximum) version.
	APIVersion() string
	// Ping checks that the engine is reachable. The first successful Ping
	// negotiates the API version down to the engine's version if needed
	// (unless DOCKER_API_VERSION pins it); later Pings do not renegotiate.
	// After reconnecting to an engine that may have been downgraded, create
	// a new Service rather than reusing this one.
	Ping(ctx context.Context) (PingResult, error)
	// Close releases the resources held by the service.
	Close() error

	// ContainerList lists containers; with opts.All it includes stopped ones.
	ContainerList(ctx context.Context, opts ContainerListOptions) ([]container.Summary, error)
	// ContainerInspect returns the decoded inspection data and the raw JSON.
	ContainerInspect(ctx context.Context, id string) (ContainerInspectResult, error)
	ContainerStart(ctx context.Context, id string) error
	// ContainerStop stops a container using the engine's default timeout.
	ContainerStop(ctx context.Context, id string) error
	// ContainerRestart restarts a container using the engine's default timeout.
	ContainerRestart(ctx context.Context, id string) error
	ContainerRemove(ctx context.Context, id string, opts ContainerRemoveOptions) error
	// ContainerLogs returns the container's raw log stream. For containers
	// without a TTY the stream is multiplexed and must be demultiplexed with
	// github.com/moby/moby/api/pkg/stdcopy. The caller must close it; it is
	// also closed when ctx is cancelled.
	ContainerLogs(ctx context.Context, id string, opts LogsOptions) (io.ReadCloser, error)

	// ImageList lists top-level images, including dangling ones.
	ImageList(ctx context.Context) ([]image.Summary, error)
	// ImageInspect returns the decoded inspection data and the raw JSON.
	// ref is an image ID or reference.
	ImageInspect(ctx context.Context, ref string) (ImageInspectResult, error)
	// ImagePull starts an anonymous pull of ref (a missing tag means
	// "latest"). The pull runs until the returned stream is drained, closed,
	// or ctx is cancelled.
	ImagePull(ctx context.Context, ref string) (PullStream, error)
	// ImageRemove removes ref (an image ID or reference). Removing a
	// reference that shares its image with other tags only untags it.
	ImageRemove(ctx context.Context, ref string, opts ImageRemoveOptions) ([]image.DeleteResponse, error)
	// ImagePruneDangling removes dangling images not used by any container,
	// running or stopped. It never removes tagged images.
	ImagePruneDangling(ctx context.Context) (image.PruneReport, error)

	VolumeList(ctx context.Context) ([]volume.Volume, error)
	// VolumeInspect returns the decoded inspection data and the raw JSON.
	VolumeInspect(ctx context.Context, name string) (VolumeInspectResult, error)
	// VolumeCreate creates a volume with the local driver. An empty name
	// lets the engine generate one. The engine treats creating an existing
	// name as a no-op that returns the existing volume.
	VolumeCreate(ctx context.Context, name string) (volume.Volume, error)
	VolumeRemove(ctx context.Context, name string, opts VolumeRemoveOptions) error

	// ExecCreate creates a non-interactive exec instance (no TTY, no
	// attached streams) running cmd in the container, and returns its ID.
	ExecCreate(ctx context.Context, containerID string, cmd []string) (string, error)
	// ExecStart starts an exec instance detached; use ExecInspect to observe
	// its progress and exit code.
	ExecStart(ctx context.Context, execID string) error
	ExecInspect(ctx context.Context, execID string) (ExecInspectResult, error)

	// Events subscribes to container, image and volume events. It blocks
	// until the engine has accepted the subscription (or failed to), so it
	// must not be called on the UI goroutine, and ctx should carry a timeout
	// or be cancellable. Once it returns, the subscription is active: a
	// caller that subscribes before listing resources misses no change.
	// Messages is never closed; Err receives exactly one error when the
	// stream ends (the context's error if ctx was cancelled) and is then
	// closed.
	Events(ctx context.Context) EventStream
}

// PingResult is the outcome of a successful Ping.
type PingResult struct {
	// APIVersion is the engine's maximum supported API version.
	APIVersion string
	// OSType is the engine's operating system, for example "linux".
	OSType string
}

// ContainerListOptions configures ContainerList.
type ContainerListOptions struct {
	// All includes stopped containers.
	All bool
}

// ContainerRemoveOptions configures ContainerRemove.
type ContainerRemoveOptions struct {
	// Force kills a running, paused or restarting container before removing it.
	Force bool
	// RemoveVolumes also removes the anonymous volumes of the container.
	RemoveVolumes bool
}

// ImageRemoveOptions configures ImageRemove.
type ImageRemoveOptions struct {
	// Force removes the image even when it is in use or has several tags.
	Force bool
	// PruneChildren also removes untagged parent images, like `docker rmi`.
	PruneChildren bool
}

// VolumeRemoveOptions configures VolumeRemove.
type VolumeRemoveOptions struct {
	// Force removes the volume even if the driver reports an error.
	Force bool
}

// LogsOptions configures ContainerLogs.
type LogsOptions struct {
	// Stdout and Stderr select the streams to read. If both are false, both
	// streams are read.
	Stdout, Stderr bool
	// Follow keeps the stream open and delivers new output as it is written.
	Follow bool
	// Tail limits the initial backlog to the last Tail log entries (lines).
	// Zero or a negative value reads the whole history.
	Tail int
}

// ContainerInspectResult holds a container's inspection data.
type ContainerInspectResult struct {
	Container container.InspectResponse
	// Raw is the engine's JSON response, unmodified.
	Raw json.RawMessage
}

// ImageInspectResult holds an image's inspection data.
type ImageInspectResult struct {
	Image image.InspectResponse
	// Raw is the engine's JSON response, unmodified.
	Raw json.RawMessage
}

// VolumeInspectResult holds a volume's inspection data.
type VolumeInspectResult struct {
	Volume volume.Volume
	// Raw is the engine's JSON response, unmodified.
	Raw json.RawMessage
}

// ExecInspectResult is the state of an exec instance.
type ExecInspectResult struct {
	Running bool
	// ExitCode is meaningful only once Running is false and the process
	// has started (Pid != 0).
	ExitCode int
	Pid      int
}

// EventStream delivers engine events. See Service.Events.
type EventStream struct {
	Messages <-chan events.Message
	Err      <-chan error
}

// PullStream is an in-progress image pull.
type PullStream interface {
	// Messages decodes the pull progress. The stream is closed when the
	// sequence ends, when the consumer stops iterating, or when ctx is
	// cancelled. A failed pull yields a message whose Error is set; transport
	// and decoding failures are yielded as errors.
	Messages(ctx context.Context) iter.Seq2[jsonstream.Message, error]
	// Close aborts the pull if it is still running. It is safe to call more
	// than once.
	Close() error
}
