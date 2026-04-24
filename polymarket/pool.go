package polymarket

import (
	"context"
	"hash/fnv"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

// WSMember is the minimal contract a Pool entry satisfies. MarketWS and
// UserWS both implement it.
type WSMember interface {
	Run(ctx context.Context)
	Connected() bool
	SetEventLog(el WSEventLogger)
	SetFilter(fn func(raw []byte) bool)
	SetOnConnect(fn func())
	SetOnDisconnect(fn func())
}

// Pool is a thin wrapper around N parallel WS members of the same type.
// Identical frames arriving from any two members collapse to one delivery:
// the first frame with a given content hash flows through, later siblings
// drop before dispatch. No TTL, no timers — a bounded hash set resets when
// it fills, which at realistic rates preserves seconds of history.
//
// Optional onAllDown fires when the last member disconnects (domain hook,
// e.g. clear stale book atomics).
type Pool[T WSMember] struct {
	members []T

	mu       sync.Mutex
	seen     map[uint64]struct{}
	capacity int

	connCount atomic.Int32
	accepted  atomic.Int64
	dropped   atomic.Int64

	onAllDown func()
	onFirstUp func()
}

type PoolOption[T WSMember] func(*Pool[T])

// WithOnAllDown fires when the last active member disconnects.
func WithOnAllDown[T WSMember](fn func()) PoolOption[T] {
	return func(p *Pool[T]) { p.onAllDown = fn }
}

// WithOnFirstUp fires when the first member comes up from an all-down state.
func WithOnFirstUp[T WSMember](fn func()) PoolOption[T] {
	return func(p *Pool[T]) { p.onFirstUp = fn }
}

// WithCapacity sets the dedup set's max size (default 4096). The set resets
// when full, so capacity bounds both memory and max history window.
func WithCapacity[T WSMember](n int) PoolOption[T] {
	return func(p *Pool[T]) {
		if n > 0 {
			p.capacity = n
		}
	}
}

// NewPool wires dedup and connection-tracking into each member and returns
// the pool. Members must outlive the pool.
func NewPool[T WSMember](members []T, opts ...PoolOption[T]) *Pool[T] {
	p := &Pool[T]{
		members:  members,
		capacity: 4096,
	}
	for _, o := range opts {
		o(p)
	}
	p.seen = make(map[uint64]struct{}, p.capacity)
	for _, m := range members {
		m.SetFilter(p.accept)
		m.SetOnConnect(p.handleConnect)
		m.SetOnDisconnect(p.handleDisconnect)
	}
	return p
}

// Run spawns one goroutine per member and blocks until ctx is done.
func (p *Pool[T]) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i, m := range p.members {
		wg.Add(1)
		go func(idx int, w T) {
			defer wg.Done()
			log.Info().Int("instance", idx).Int("size", len(p.members)).Msg("[POOL] member started")
			w.Run(ctx)
		}(i, m)
	}
	wg.Wait()
}

// Connected reports whether at least one member currently has a live socket.
func (p *Pool[T]) Connected() bool { return p.connCount.Load() > 0 }

// Members exposes the underlying slice for cases where a caller needs the
// raw list. Prefer Each for fan-out.
func (p *Pool[T]) Members() []T { return p.members }

// Each applies fn to every member. This is the single primitive for domain
// fan-out — callers express per-member operations as a closure, e.g.:
//
//	pool.Each(func(ws *polymarket.MarketWS) { ws.SubscribeTokens(ids) })
//	pool.Each(func(ws *polymarket.UserWS)   { ws.SetOnFill(onFill) })
func (p *Pool[T]) Each(fn func(T)) {
	for _, m := range p.members {
		fn(m)
	}
}

// SetEventLog fans out the event sink to every member.
func (p *Pool[T]) SetEventLog(el WSEventLogger) {
	p.Each(func(m T) { m.SetEventLog(el) })
}

// Stats returns cumulative (accepted, dropped) frames. In a 2-member pool
// both healthy, dropped ≈ accepted.
func (p *Pool[T]) Stats() (accepted, dropped int64) {
	return p.accepted.Load(), p.dropped.Load()
}

// accept is the filter plugged into each member. First frame with a given
// content hash wins; siblings drop.
func (p *Pool[T]) accept(raw []byte) bool {
	h := fnv.New64a()
	_, _ = h.Write(raw)
	k := h.Sum64()
	p.mu.Lock()
	if _, ok := p.seen[k]; ok {
		p.mu.Unlock()
		p.dropped.Add(1)
		return false
	}
	if len(p.seen) >= p.capacity {
		p.seen = make(map[uint64]struct{}, p.capacity)
	}
	p.seen[k] = struct{}{}
	p.mu.Unlock()
	p.accepted.Add(1)
	return true
}

func (p *Pool[T]) handleConnect() {
	n := p.connCount.Add(1)
	if n == 1 && p.onFirstUp != nil {
		p.onFirstUp()
	}
	log.Info().Int32("active", n).Int("size", len(p.members)).Msg("[POOL] up")
}

func (p *Pool[T]) handleDisconnect() {
	n := p.connCount.Add(-1)
	log.Warn().Int32("active", n).Int("size", len(p.members)).Msg("[POOL] down")
	if n <= 0 && p.onAllDown != nil {
		p.onAllDown()
	}
}

