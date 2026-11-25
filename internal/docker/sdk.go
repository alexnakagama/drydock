package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"
)

// localVolumeDriver is the engine's built-in volume driver.
const localVolumeDriver = "local"

// SDKService implements Service with the Docker Engine Go SDK.
type SDKService struct {
	cli *client.Client
}

var _ Service = (*SDKService)(nil)

// NewSDKService creates a service for the engine configured by the
// environment: DOCKER_HOST (default: the platform's local socket),
// DOCKER_TLS_VERIFY and DOCKER_CERT_PATH for TLS, and DOCKER_API_VERSION to
// pin an API version. Unless pinned, the API version is negotiated with the
// engine on the first request. No connection is made until a method is
// called.
func NewSDKService(userAgent string) (*SDKService, error) {
	opts := []client.Opt{client.FromEnv}
	if userAgent != "" {
		opts = append(opts, client.WithUserAgent(userAgent))
	}
	cli, err := client.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("configure Docker client: %w", err)
	}
	return &SDKService{cli: cli}, nil
}

// Host implements Service.
func (s *SDKService) Host() string { return s.cli.DaemonHost() }

// APIVersion implements Service.
func (s *SDKService) APIVersion() string { return s.cli.ClientVersion() }

// Close implements Service.
func (s *SDKService) Close() error { return s.cli.Close() }

// Ping implements Service.
func (s *SDKService) Ping(ctx context.Context) (PingResult, error) {
	res, err := s.cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true})
	if err != nil {
		return PingResult{}, err
	}
	return PingResult{APIVersion: res.APIVersion, OSType: res.OSType}, nil
}

// ContainerList implements Service.
func (s *SDKService) ContainerList(ctx context.Context, opts ContainerListOptions) ([]container.Summary, error) {
	res, err := s.cli.ContainerList(ctx, client.ContainerListOptions{All: opts.All})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

// ContainerInspect implements Service.
func (s *SDKService) ContainerInspect(ctx context.Context, id string) (ContainerInspectResult, error) {
	if err := ValidateContainerID(id); err != nil {
		return ContainerInspectResult{}, err
	}
	res, err := s.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerInspectResult{}, err
	}
	return ContainerInspectResult{Container: res.Container, Raw: res.Raw}, nil
}

// ContainerStart implements Service.
func (s *SDKService) ContainerStart(ctx context.Context, id string) error {
	if err := ValidateContainerID(id); err != nil {
		return err
	}
	_, err := s.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

// ContainerStop implements Service.
func (s *SDKService) ContainerStop(ctx context.Context, id string) error {
	if err := ValidateContainerID(id); err != nil {
		return err
	}
	_, err := s.cli.ContainerStop(ctx, id, client.ContainerStopOptions{})
	return err
}

// ContainerRestart implements Service.
func (s *SDKService) ContainerRestart(ctx context.Context, id string) error {
	if err := ValidateContainerID(id); err != nil {
		return err
	}
	_, err := s.cli.ContainerRestart(ctx, id, client.ContainerRestartOptions{})
	return err
}

// ContainerRemove implements Service.
func (s *SDKService) ContainerRemove(ctx context.Context, id string, opts ContainerRemoveOptions) error {
	if err := ValidateContainerID(id); err != nil {
		return err
	}
	_, err := s.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{
		Force:         opts.Force,
		RemoveVolumes: opts.RemoveVolumes,
	})
	return err
}

// ContainerLogs implements Service.
func (s *SDKService) ContainerLogs(ctx context.Context, id string, opts LogsOptions) (io.ReadCloser, error) {
	if err := ValidateContainerID(id); err != nil {
		return nil, err
	}
	stdout, stderr := opts.Stdout, opts.Stderr
	if !stdout && !stderr {
		stdout, stderr = true, true
	}
	tail := "all"
	if opts.Tail > 0 {
		tail = strconv.Itoa(opts.Tail)
	}
	return s.cli.ContainerLogs(ctx, id, client.ContainerLogsOptions{
		ShowStdout: stdout,
		ShowStderr: stderr,
		Follow:     opts.Follow,
		Tail:       tail,
	})
}

// ContainerStats implements Service.
func (s *SDKService) ContainerStats(ctx context.Context, id string) (StatsStream, error) {
	if err := ValidateContainerID(id); err != nil {
		return nil, err
	}
	res, err := s.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		return nil, err
	}
	return &sdkStatsStream{body: res.Body}, nil
}

// ImageList implements Service.
func (s *SDKService) ImageList(ctx context.Context) ([]image.Summary, error) {
	res, err := s.cli.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

// ImageInspect implements Service.
func (s *SDKService) ImageInspect(ctx context.Context, ref string) (ImageInspectResult, error) {
	if err := ValidateImageRef(ref); err != nil {
		return ImageInspectResult{}, err
	}
	var raw bytes.Buffer
	res, err := s.cli.ImageInspect(ctx, ref, client.ImageInspectWithRawResponse(&raw))
	if err != nil {
		return ImageInspectResult{}, err
	}
	return ImageInspectResult{Image: res.InspectResponse, Raw: raw.Bytes()}, nil
}

// ImagePull implements Service. No registry credentials are sent.
func (s *SDKService) ImagePull(ctx context.Context, ref string) (PullStream, error) {
	if err := ValidateImageRef(ref); err != nil {
		return nil, err
	}
	res, err := s.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return nil, err
	}
	return sdkPullStream{res}, nil
}

// ImageRemove implements Service.
func (s *SDKService) ImageRemove(ctx context.Context, ref string, opts ImageRemoveOptions) ([]image.DeleteResponse, error) {
	if err := ValidateImageRef(ref); err != nil {
		return nil, err
	}
	res, err := s.cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{
		Force:         opts.Force,
		PruneChildren: opts.PruneChildren,
	})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

// ImagePruneDangling implements Service.
func (s *SDKService) ImagePruneDangling(ctx context.Context) (image.PruneReport, error) {
	res, err := s.cli.ImagePrune(ctx, client.ImagePruneOptions{
		Filters: make(client.Filters).Add("dangling", "true"),
	})
	if err != nil {
		return image.PruneReport{}, err
	}
	return res.Report, nil
}

// VolumeList implements Service.
func (s *SDKService) VolumeList(ctx context.Context) ([]volume.Volume, error) {
	res, err := s.cli.VolumeList(ctx, client.VolumeListOptions{})
	if err != nil {
		return nil, err
	}
	return res.Items, nil
}

// VolumeInspect implements Service.
func (s *SDKService) VolumeInspect(ctx context.Context, name string) (VolumeInspectResult, error) {
	if err := ValidateVolumeName(name); err != nil {
		return VolumeInspectResult{}, err
	}
	res, err := s.cli.VolumeInspect(ctx, name, client.VolumeInspectOptions{})
	if err != nil {
		return VolumeInspectResult{}, err
	}
	return VolumeInspectResult{Volume: res.Volume, Raw: res.Raw}, nil
}

// VolumeCreate implements Service.
func (s *SDKService) VolumeCreate(ctx context.Context, name string) (volume.Volume, error) {
	if name != "" {
		if err := ValidateVolumeName(name); err != nil {
			return volume.Volume{}, err
		}
	}
	res, err := s.cli.VolumeCreate(ctx, client.VolumeCreateOptions{Name: name, Driver: localVolumeDriver})
	if err != nil {
		return volume.Volume{}, err
	}
	return res.Volume, nil
}

// VolumeRemove implements Service.
func (s *SDKService) VolumeRemove(ctx context.Context, name string, opts VolumeRemoveOptions) error {
	if err := ValidateVolumeName(name); err != nil {
		return err
	}
	_, err := s.cli.VolumeRemove(ctx, name, client.VolumeRemoveOptions{Force: opts.Force})
	return err
}

// ExecCreate implements Service.
func (s *SDKService) ExecCreate(ctx context.Context, containerID string, cmd []string) (string, error) {
	if err := ValidateContainerID(containerID); err != nil {
		return "", err
	}
	if len(cmd) == 0 {
		return "", fmt.Errorf("exec: empty command")
	}
	res, err := s.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{Cmd: cmd})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// ExecStart implements Service.
func (s *SDKService) ExecStart(ctx context.Context, execID string) error {
	if err := ValidateExecID(execID); err != nil {
		return err
	}
	_, err := s.cli.ExecStart(ctx, execID, client.ExecStartOptions{Detach: true})
	return err
}

// ExecInspect implements Service.
func (s *SDKService) ExecInspect(ctx context.Context, execID string) (ExecInspectResult, error) {
	if err := ValidateExecID(execID); err != nil {
		return ExecInspectResult{}, err
	}
	res, err := s.cli.ExecInspect(ctx, execID, client.ExecInspectOptions{})
	if err != nil {
		return ExecInspectResult{}, err
	}
	return ExecInspectResult{Running: res.Running, ExitCode: res.ExitCode, Pid: res.PID}, nil
}

// Events implements Service.
func (s *SDKService) Events(ctx context.Context) EventStream {
	res := s.cli.Events(ctx, client.EventsListOptions{
		Filters: make(client.Filters).Add("type", "container", "image", "volume"),
	})
	return EventStream{Messages: res.Messages, Err: res.Err}
}

// sdkPullStream adapts the SDK's pull response to PullStream.
type sdkPullStream struct {
	res client.ImagePullResponse
}

func (p sdkPullStream) Messages(ctx context.Context) iter.Seq2[jsonstream.Message, error] {
	return p.res.JSONMessages(ctx)
}

func (p sdkPullStream) Close() error { return p.res.Close() }

// maxStatsRecord bounds the bytes read while decoding one stats record, on
// top of what the decoder already buffered, so that a misbehaving endpoint
// cannot make the decoder buffer without limit. A real record is a few
// kilobytes.
const maxStatsRecord = 1 << 20

// errStatsRecordTooLarge ends a stats stream whose record exceeds
// maxStatsRecord.
var errStatsRecordTooLarge = errors.New("docker: stats record too large")

// sdkStatsStream decodes the engine's stats JSON sequence.
type sdkStatsStream struct {
	body io.ReadCloser

	once     sync.Once
	closed   atomic.Bool
	closeErr error
}

func (st *sdkStatsStream) Samples(ctx context.Context) iter.Seq2[container.StatsResponse, error] {
	return func(yield func(container.StatsResponse, error) bool) {
		defer func() { _ = st.Close() }()
		if err := ctx.Err(); err != nil {
			yield(container.StatsResponse{}, err)
			return
		}
		if st.closed.Load() {
			yield(container.StatsResponse{}, ErrStatsStreamClosed)
			return
		}
		// Closing the body unblocks a pending read when ctx is cancelled.
		stop := context.AfterFunc(ctx, func() { _ = st.Close() })
		defer stop()

		src := &recordLimitReader{r: st.body}
		dec := json.NewDecoder(src)
		for {
			src.remaining = maxStatsRecord
			var sample container.StatsResponse
			if err := dec.Decode(&sample); err != nil {
				if cerr := ctx.Err(); cerr != nil {
					err = cerr
				} else if st.closed.Load() {
					err = ErrStatsStreamClosed
				} else if errors.Is(err, io.EOF) {
					return // the engine ended the stream: the container stopped
				}
				yield(container.StatsResponse{}, err)
				return
			}
			if !yield(sample, nil) {
				return
			}
		}
	}
}

func (st *sdkStatsStream) Close() error {
	st.once.Do(func() {
		st.closed.Store(true) // before the body closes, so a failing read sees it
		st.closeErr = st.body.Close()
	})
	return st.closeErr
}

// recordLimitReader fails reads once remaining bytes are used up. The
// caller resets remaining before decoding each record.
type recordLimitReader struct {
	r         io.Reader
	remaining int64
}

func (l *recordLimitReader) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		return 0, errStatsRecordTooLarge
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.r.Read(p)
	l.remaining -= int64(n)
	return n, err
}
