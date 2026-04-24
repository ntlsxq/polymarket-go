package polymarket

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

// WSMember is the interface both MarketWS and UserWS satisfy — the minimal
// surface a Pool needs for run/lifecycle and connection tracking. SetFilter
// is how a shared Deduper is plugged in for redundancy dedup.
type WSMember interface {
	Run(ctx context.Context)
	Connected() bool
	SetEventLog(el WSEventLogger)
	SetFilter(fn func(raw []byte) bool)
	SetOnConnect(fn func())
	SetOnDisconnect(fn func())
}

// Pool runs N parallel WS instances of the same type for redundancy,
// deduplicating incoming frames by content hash (first-to-arrive wins).
// Aggregates connection count so Connected() reports any-up. Optional
// onAllDown fires when the last member drops — domain-specific recovery
// (e.g. MarketWS pool clears book atomics so stale prices don't drive
// decisions).
type Pool[T WSMember] struct {
	members   []T
	dedup     *Deduper
	connCount atomic.Int32
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

// NewPool wires members to a shared Deduper and connection-count tracker.
// Members must outlive the pool (pool doesn't own them). Pass opts to attach
// domain-specific recovery hooks.
func NewPool[T WSMember](dedup *Deduper, members []T, opts ...PoolOption[T]) *Pool[T] {
	p := &Pool[T]{members: members, dedup: dedup}
	for _, o := range opts {
		o(p)
	}
	for _, m := range members {
		m.SetFilter(dedup.Accept)
		m.SetOnConnect(p.handleConnect)
		m.SetOnDisconnect(p.handleDisconnect)
	}
	return p
}

// Run blocks until ctx is done, spawning one goroutine per member.
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

// Members returns the slice of members for domain-specific fan-out (e.g.
// MarketWS.SubscribeTokens on each).
func (p *Pool[T]) Members() []T { return p.members }

// SetEventLog fans out the event sink to every member.
func (p *Pool[T]) SetEventLog(el WSEventLogger) {
	for _, m := range p.members {
		m.SetEventLog(el)
	}
}

// Dedup exposes the deduper for stats/observability.
func (p *Pool[T]) Dedup() *Deduper { return p.dedup }

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
