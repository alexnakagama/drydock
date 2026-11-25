package docker

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
)

// trackedBody is an io.ReadCloser that records Close and unblocks reads.
type trackedBody struct {
	r      io.Reader
	closed atomic.Int32
	done   chan struct{}
}

func newTrackedBody(r io.Reader) *trackedBody { return &trackedBody{r: r, done: make(chan struct{})} }

func (b *trackedBody) Read(p []byte) (int, error) {
	if b.closed.Load() > 0 {
		return 0, errors.New("read on closed body")
	}
	return b.r.Read(p)
}

func (b *trackedBody) Close() error {
	if b.closed.Add(1) == 1 {
		close(b.done)
	}
	return nil
}

// blockingReader blocks until done is closed.
type blockingReader struct{ done <-chan struct{} }

func (r blockingReader) Read([]byte) (int, error) {
	<-r.done
	return 0, errors.New("read on closed body")
}

func collectStats(ctx context.Context, st StatsStream) ([]container.StatsResponse, error) {
	var out []container.StatsResponse
	for s, err := range st.Samples(ctx) {
		if err != nil {
			return out, err
		}
		out = append(out, s)
	}
	return out, nil
}

func TestStatsStreamDecodesUntilEOF(t *testing.T) {
	t.Parallel()
	body := newTrackedBody(strings.NewReader(
		`{"id":"a","precpu_stats":{"cpu_usage":{"total_usage":0}},"cpu_stats":{"cpu_usage":{"total_usage":10},"system_cpu_usage":100}}` + "\n" +
			`{"id":"a","precpu_stats":{"cpu_usage":{"total_usage":10},"system_cpu_usage":100},"cpu_stats":{"cpu_usage":{"total_usage":20},"system_cpu_usage":200}}` + "\n"))
	st := &sdkStatsStream{body: body}
	got, err := collectStats(t.Context(), st)
	if err != nil {
		t.Fatalf("Samples: %v", err)
	}
	if len(got) != 2 || got[0].PreCPUStats.SystemUsage != 0 || got[1].PreCPUStats.SystemUsage != 100 {
		t.Errorf("samples = %+v", got)
	}
	if body.closed.Load() != 1 {
		t.Errorf("body closed %d times, want 1", body.closed.Load())
	}
	if err := st.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if body.closed.Load() != 1 {
		t.Errorf("Close is not idempotent: body closed %d times", body.closed.Load())
	}
}

func TestStatsStreamMalformedJSON(t *testing.T) {
	t.Parallel()
	st := &sdkStatsStream{body: newTrackedBody(strings.NewReader(`{"id":"a"}{"id":`))}
	got, err := collectStats(t.Context(), st)
	if len(got) != 1 || err == nil || errors.Is(err, io.EOF) {
		t.Errorf("got %d samples, err %v; want 1 sample and a decoding error", len(got), err)
	}
}

func TestStatsStreamRecordTooLarge(t *testing.T) {
	t.Parallel()
	huge := `{"id":"` + strings.Repeat("x", 2*maxStatsRecord) + `"}`
	st := &sdkStatsStream{body: newTrackedBody(strings.NewReader(`{"id":"a"}` + huge))}
	got, err := collectStats(t.Context(), st)
	if len(got) != 1 || !errors.Is(err, errStatsRecordTooLarge) {
		t.Errorf("got %d samples, err %v; want 1 sample and errStatsRecordTooLarge", len(got), err)
	}
}

func TestStatsStreamManyRecordsWithinLimit(t *testing.T) {
	t.Parallel()
	// The per-record limit is reset for each record, so a long stream of
	// small records is never cut off.
	rec := `{"id":"` + strings.Repeat("x", 4096) + `"}` + "\n"
	n := 2*maxStatsRecord/len(rec) + 1
	st := &sdkStatsStream{body: newTrackedBody(strings.NewReader(strings.Repeat(rec, n)))}
	got, err := collectStats(t.Context(), st)
	if err != nil || len(got) != n {
		t.Errorf("got %d samples, err %v; want %d and nil", len(got), err, n)
	}
}

func TestStatsStreamCancelUnblocksRead(t *testing.T) {
	t.Parallel()
	body := newTrackedBody(nil)
	body.r = blockingReader{done: body.done}
	st := &sdkStatsStream{body: body}
	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() {
		_, err := collectStats(ctx, st)
		errc <- err
	}()
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling ctx did not end the iteration")
	}
	if body.closed.Load() != 1 {
		t.Errorf("body closed %d times, want 1", body.closed.Load())
	}
}

func TestStatsStreamCancelledBeforeIteration(t *testing.T) {
	t.Parallel()
	body := newTrackedBody(strings.NewReader(`{"id":"a"}`))
	st := &sdkStatsStream{body: body}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := collectStats(ctx, st)
	if len(got) != 0 || !errors.Is(err, context.Canceled) {
		t.Errorf("got %d samples, err %v; want none and context.Canceled", len(got), err)
	}
	if body.closed.Load() != 1 {
		t.Error("body not closed")
	}
}

func TestStatsStreamConsumerStops(t *testing.T) {
	t.Parallel()
	body := newTrackedBody(strings.NewReader(`{"id":"a"}{"id":"b"}`))
	st := &sdkStatsStream{body: body}
	for _, err := range st.Samples(t.Context()) {
		if err != nil {
			t.Fatalf("Samples: %v", err)
		}
		break
	}
	if body.closed.Load() != 1 {
		t.Error("stopping the iteration did not close the body")
	}
}

func TestStatsStreamCloseDuringRead(t *testing.T) {
	t.Parallel()
	body := newTrackedBody(nil)
	body.r = blockingReader{done: body.done}
	st := &sdkStatsStream{body: body}
	errc := make(chan error, 1)
	go func() {
		_, err := collectStats(t.Context(), st)
		errc <- err
	}()
	_ = st.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrStatsStreamClosed) {
			t.Errorf("err = %v, want ErrStatsStreamClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not end the iteration")
	}
}

func TestStatsStreamSecondIteration(t *testing.T) {
	t.Parallel()
	st := &sdkStatsStream{body: newTrackedBody(strings.NewReader(`{"id":"a"}`))}
	if _, err := collectStats(t.Context(), st); err != nil {
		t.Fatalf("first iteration: %v", err)
	}
	if got, err := collectStats(t.Context(), st); len(got) != 0 || !errors.Is(err, ErrStatsStreamClosed) {
		t.Errorf("second iteration: %d samples, err %v; want none and ErrStatsStreamClosed", len(got), err)
	}
}
