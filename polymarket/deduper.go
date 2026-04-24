package polymarket

import (
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

// Deduper deduplicates raw WS frames across N parallel streams by content
// hash. First caller with a given hash wins (Accept → true); later callers
// with the same hash drop (Accept → false).
//
// Use case: N WebSocket instances subscribed to the same upstream source
// (for redundancy). Each frame arrives N times; we want to process it once.
//
// Correctness: relies on the upstream sending byte-identical frames to all
// subscribers. Polymarket's CLOB does; if some other source doesn't, callers
// must supply a normalized key via AcceptKey instead of raw frame hashing.
//
// Time window: sliding TTL via two rotating buckets. At time t, we remember
// all hashes seen within (t - ttl, t]. Memory is bounded by peak frame rate
// × ttl. At 1000 ev/s × 1s TTL = ~16 KB peak.
type Deduper struct {
	mu      sync.Mutex
	now     map[uint64]struct{}
	prev    map[uint64]struct{}
	rotAt   time.Time
	halfTTL time.Duration

	accepted atomic.Int64
	dropped  atomic.Int64
}

// NewDeduper creates a deduper with the given sliding window. 1s is a good
// default for WS redundancy — upstream usually fans out within ms.
func NewDeduper(ttl time.Duration) *Deduper {
	if ttl <= 0 {
		ttl = time.Second
	}
	half := ttl / 2
	if half <= 0 {
		half = ttl
	}
	return &Deduper{
		now:     make(map[uint64]struct{}, 1024),
		prev:    make(map[uint64]struct{}, 1024),
		rotAt:   time.Now().Add(half),
		halfTTL: half,
	}
}

// Accept hashes raw and returns true iff this hash has not been seen within
// the TTL window. First caller wins. Safe for concurrent use.
func (d *Deduper) Accept(raw []byte) bool {
	if d == nil {
		return true
	}
	h := fnv.New64a()
	_, _ = h.Write(raw)
	return d.AcceptKey(h.Sum64())
}

// AcceptKey takes a pre-computed 64-bit key. Useful when the caller has a
// semantic key (e.g. orderID+type) more precise than raw bytes.
func (d *Deduper) AcceptKey(key uint64) bool {
	if d == nil {
		return true
	}
	d.mu.Lock()
	if time.Now().After(d.rotAt) {
		d.prev = d.now
		d.now = make(map[uint64]struct{}, len(d.prev)+64)
		d.rotAt = time.Now().Add(d.halfTTL)
	}
	if _, ok := d.now[key]; ok {
		d.mu.Unlock()
		d.dropped.Add(1)
		return false
	}
	if _, ok := d.prev[key]; ok {
		d.mu.Unlock()
		d.dropped.Add(1)
		return false
	}
	d.now[key] = struct{}{}
	d.mu.Unlock()
	d.accepted.Add(1)
	return true
}

// Stats returns cumulative (accepted, dropped) counts since construction.
// Drop ratio = dropped / (accepted + dropped) = fraction of frames absorbed
// by redundant streams. In a 2-WS pool both fully connected, expect ~50%.
func (d *Deduper) Stats() (accepted, dropped int64) {
	if d == nil {
		return 0, 0
	}
	return d.accepted.Load(), d.dropped.Load()
}
