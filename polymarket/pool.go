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

// Members exposes the underlying slice for domain-specific fan-out
// (e.g. MarketWS.SubscribeTokens on each).
func (p *Pool[T]) Members() []T { return p.members }

// SetEventLog fans out the event sink to every member.
func (p *Pool[T]) SetEventLog(el WSEventLogger) {
	for _, m := range p.members {
		m.SetEventLog(el)
	}
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

// UserPool is Pool[*UserWS] + domain fan-outs: setting an order/fill
// handler on the pool installs it on every member. Run/Connected/Stats
// etc. come through the embedded *Pool.
type UserPool struct {
	*Pool[*UserWS]
}

// NewUserPool wraps the generic Pool with UserWS-specific fan-outs.
func NewUserPool(members []*UserWS, opts ...PoolOption[*UserWS]) *UserPool {
	return &UserPool{Pool: NewPool[*UserWS](members, opts...)}
}

// SetOnOrder installs fn on every member.
func (p *UserPool) SetOnOrder(fn func(OrderEvent)) {
	for _, ws := range p.Members() {
		ws.SetOnOrder(fn)
	}
}

// SetOnFill installs fn on every member.
func (p *UserPool) SetOnFill(fn func(Fill)) {
	for _, ws := range p.Members() {
		ws.SetOnFill(fn)
	}
}

// SetOnReconnect installs fn on every member.
func (p *UserPool) SetOnReconnect(fn func()) {
	for _, ws := range p.Members() {
		ws.SetOnReconnect(fn)
	}
}

// MarketPool is Pool[*MarketWS] + MarketWS-specific fan-outs (subscribe /
// unsubscribe tokens across all members).
type MarketPool struct {
	*Pool[*MarketWS]
}

// NewMarketPool wraps the generic Pool with MarketWS-specific fan-outs.
func NewMarketPool(members []*MarketWS, opts ...PoolOption[*MarketWS]) *MarketPool {
	return &MarketPool{Pool: NewPool[*MarketWS](members, opts...)}
}

// SubscribeTokens fans out the subscription to every member.
func (p *MarketPool) SubscribeTokens(tokenIDs []string) {
	for _, ws := range p.Members() {
		ws.SubscribeTokens(tokenIDs)
	}
}

// UnsubscribeTokens fans out the unsubscription to every member.
func (p *MarketPool) UnsubscribeTokens(tokenIDs []string) {
	for _, ws := range p.Members() {
		ws.UnsubscribeTokens(tokenIDs)
	}
}
