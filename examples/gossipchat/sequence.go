package main

// sequencer is an element from an infinite stream of monotonically increasing values, starting at 0.
type sequencer uint

// sequenceTracker tracks a potentially out-of-order stream of sequencers,
// expecting to see each value at least once.
type sequenceTracker struct {
	horizon sequencer
	seen    map[sequencer]bool
}

// See records that the given sequencer has been seen,
// and returns true if this is the first time it has been seen.
func (t *sequenceTracker) See(s sequencer) bool {
	if s < t.horizon || t.seen[s] {
		return false
	}
	if t.seen == nil {
		t.seen = make(map[sequencer]bool)
	}
	t.seen[s] = true
	for t.seen[t.horizon] {
		delete(t.seen, t.horizon)
		t.horizon++
	}
	return true
}
