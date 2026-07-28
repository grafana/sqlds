package diagnostics

import "sync"

// DefaultMaxInteractions is the interaction count a SliceRecorder keeps when no
// explicit limit is given. A dashboard-wide capture fans out over many panels
// and refIDs, so the ceiling is well above a single panel's query count while
// still bounding a runaway loop.
const DefaultMaxInteractions = 200

// SliceRecorder is a bounded, concurrency-safe Recorder that keeps
// Interactions in memory in the order they were recorded. It is the reference
// implementation of the Recorder contract: consumers that need a different
// encoding (a HAR document, NDJSON) can either wrap it or implement Recorder
// directly.
//
// Interactions are dropped once the limit is reached rather than evicting
// earlier ones, because the first failing query in a request is usually the
// interesting one. Dropped reports whether that happened, so a consumer can
// mark the capture as incomplete instead of presenting it as exhaustive.
type SliceRecorder struct {
	mu           sync.Mutex
	interactions []Interaction
	dropped      int
	max          int
}

// NewSliceRecorder returns a SliceRecorder that keeps at most max
// Interactions. A max of zero or less uses DefaultMaxInteractions.
func NewSliceRecorder(max int) *SliceRecorder {
	if max <= 0 {
		max = DefaultMaxInteractions
	}
	return &SliceRecorder{max: max}
}

// Record implements Recorder.
func (r *SliceRecorder) Record(i Interaction) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.interactions) >= r.max {
		r.dropped++
		return
	}
	r.interactions = append(r.interactions, i)
}

// Interactions returns a copy of the recorded Interactions. The copy keeps the
// caller safe from concurrent Records still in flight on other queries of the
// same request.
func (r *SliceRecorder) Interactions() []Interaction {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Interaction, len(r.interactions))
	copy(out, r.interactions)
	return out
}

// Dropped returns how many Interactions were discarded because the limit was
// reached. A non-zero value means the capture is a prefix, not the whole run.
func (r *SliceRecorder) Dropped() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}
