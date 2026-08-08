package diagnostics

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecorderFromContext(t *testing.T) {
	t.Run("a plain context has no recorder", func(t *testing.T) {
		got, ok := RecorderFromContext(context.Background())
		require.False(t, ok)
		require.Nil(t, got)
	})

	t.Run("a nil context has no recorder", func(t *testing.T) {
		// Callers pass whatever context the query arrived with; a nil context
		// must read as "capture off" rather than panic on the query path.
		//nolint:staticcheck // deliberately exercising the nil-context case
		got, ok := RecorderFromContext(nil)
		require.False(t, ok)
		require.Nil(t, got)
	})

	t.Run("round-trips the installed recorder", func(t *testing.T) {
		rec := NewSliceRecorder(0)
		got, ok := RecorderFromContext(WithRecorder(context.Background(), rec))
		require.True(t, ok)
		require.Same(t, rec, got)
	})
}

func TestWithRecorder_NilIsNotCaptureOn(t *testing.T) {
	// Wiring capture unconditionally is a common host pattern, so a nil Recorder
	// must leave the context untouched instead of installing a typed nil that
	// later reads as "capture on".
	ctx := context.Background()
	got := WithRecorder(ctx, nil)
	require.Equal(t, ctx, got)

	_, ok := RecorderFromContext(got)
	require.False(t, ok)
}

func TestSliceRecorder(t *testing.T) {
	t.Run("keeps interactions in order", func(t *testing.T) {
		rec := NewSliceRecorder(0)
		rec.Record(Interaction{RefID: "A"})
		rec.Record(Interaction{RefID: "B"})

		got := rec.Interactions()
		require.Len(t, got, 2)
		require.Equal(t, "A", got[0].RefID)
		require.Equal(t, "B", got[1].RefID)
		require.Zero(t, rec.Dropped())
	})

	t.Run("defaults the limit when given zero or less", func(t *testing.T) {
		require.Equal(t, DefaultMaxInteractions, NewSliceRecorder(0).max)
		require.Equal(t, DefaultMaxInteractions, NewSliceRecorder(-5).max)
	})

	t.Run("drops past the limit and keeps the earliest", func(t *testing.T) {
		// The first failing query in a request is usually the interesting one,
		// so the limit drops late interactions rather than evicting early ones.
		rec := NewSliceRecorder(2)
		rec.Record(Interaction{RefID: "A"})
		rec.Record(Interaction{RefID: "B"})
		rec.Record(Interaction{RefID: "C"})

		got := rec.Interactions()
		require.Len(t, got, 2)
		require.Equal(t, "A", got[0].RefID)
		require.Equal(t, "B", got[1].RefID)
		require.Equal(t, 1, rec.Dropped(), "a dropped interaction makes the capture a prefix")
	})

	t.Run("Interactions returns a copy", func(t *testing.T) {
		rec := NewSliceRecorder(0)
		rec.Record(Interaction{RefID: "A"})

		got := rec.Interactions()
		got[0].RefID = "mutated"
		require.Equal(t, "A", rec.Interactions()[0].RefID)
	})

	t.Run("is safe for concurrent use", func(t *testing.T) {
		// sqlds runs one goroutine per refID within a QueryDataRequest, so a
		// single Recorder is written to concurrently. Run with -race.
		rec := NewSliceRecorder(1000)
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rec.Record(Interaction{Kind: KindSQLQuery})
				_ = rec.Interactions()
				_ = rec.Dropped()
			}()
		}
		wg.Wait()
		require.Len(t, rec.Interactions(), 50)
	})
}
