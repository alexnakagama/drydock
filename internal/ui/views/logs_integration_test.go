//go:build integration

package views

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
	"github.com/alexnakagama/drydock/internal/store"
)

// connectedStore runs a store on the real engine until the test ends.
func connectedStore(t *testing.T) *store.Store {
	t.Helper()
	svc, err := docker.NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	connected := make(chan struct{}, 1)
	st := store.New(svc, func(m tea.Msg) {
		if c, ok := m.(store.ConnectionMsg); ok && c.State == store.StateConnected {
			select {
			case connected <- struct{}{}:
			default:
			}
		}
	})
	done := make(chan struct{})
	go func() { defer close(done); _ = st.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = svc.Close() })
	select {
	case <-connected:
	case <-time.After(10 * time.Second):
		t.Fatal("store did not connect")
	}
	return st
}

func TestIntegrationLogs(t *testing.T) {
	st := connectedStore(t)
	script := `for i in 1 2 3; do echo out-$i; echo err-$i >&2; done; printf 'no-newline'; sleep 300`
	for _, tty := range []bool{false, true} {
		name := "no tty"
		var args []string
		if tty {
			name = "tty"
			args = append(args, "-t")
		}
		t.Run(name, func(t *testing.T) {
			id := dockerRun(t, append(args, "alpine:latest", "sh", "-c", script)...)

			cfg := defaultLogsConfig()
			cfg.batchInterval = 20 * time.Millisecond
			lv := newLogsView(st, id, "it", cfg)
			go lv.stream()
			defer func() { lv.Close(); <-lv.done }()

			want := []string{"out-1", "err-1", "out-2", "err-2", "out-3", "err-3"}
			var got []string
			deadline := time.After(10 * time.Second)
			for len(got) < len(want) {
				select {
				case b := <-lv.batches:
					if b.err != nil {
						t.Fatalf("stream error: %v", b.err)
					}
					got = append(got, b.lines...)
				case <-deadline:
					t.Fatalf("got %q", got)
				}
			}
			// stdout and stderr are separate pipes in the container, so
			// only the order within each stream is guaranteed.
			var outs, errs []string
			for _, l := range got[:len(want)] {
				if strings.HasPrefix(l, "out-") {
					outs = append(outs, l)
				} else {
					errs = append(errs, l)
				}
			}
			if !slices.Equal(outs, []string{"out-1", "out-2", "out-3"}) || !slices.Equal(errs, []string{"err-1", "err-2", "err-3"}) {
				t.Errorf("lines = %q", got)
			}
		})
	}
}
