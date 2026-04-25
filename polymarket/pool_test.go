package polymarket

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// stubMember is a no-op WSMember used to construct a Pool in tests without
// touching the network. Pool only calls Set* hooks at construction; Run is
// only used when the test explicitly drives it.
type stubMember struct {
	connected   atomic.Bool
	filter      func(raw []byte) bool
	onConnect   func()
	onDisc      func()
	eventLog    WSEventLogger
	runStarted  atomic.Bool
}

func (s *stubMember) Run(ctx context.Context)                  { s.runStarted.Store(true); <-ctx.Done() }
func (s *stubMember) Connected() bool                          { return s.connected.Load() }
func (s *stubMember) SetEventLog(el WSEventLogger)             { s.eventLog = el }
func (s *stubMember) SetFilter(fn func(raw []byte) bool)       { s.filter = fn }
func (s *stubMember) SetOnConnect(fn func())                   { s.onConnect = fn }
func (s *stubMember) SetOnDisconnect(fn func())                { s.onDisc = fn }

func newStubPool(n int) (*Pool[*stubMember], []*stubMember) {
	members := make([]*stubMember, n)
	for i := range members {
		members[i] = &stubMember{}
	}
	return NewPool[*stubMember](members), members
}

func TestPoolFirstFramePassesDuplicateDrops(t *testing.T) {
	p, members := newStubPool(2)
	frame := []byte(`{"event_type":"book","x":1}`)

	if !members[0].filter(frame) {
		t.Fatal("first frame must pass")
	}
	if members[1].filter(frame) {
		t.Fatal("duplicate frame must drop")
	}

	accepted, dropped := p.Stats()
	if accepted != 1 || dropped != 1 {
		t.Fatalf("stats: accepted=%d dropped=%d", accepted, dropped)
	}
}

func TestPoolDistinctContentPassesIndependently(t *testing.T) {
	_, members := newStubPool(2)
	frame1 := []byte(`{"event_type":"book","x":1}`)
	frame2 := []byte(`{"event_type":"book","x":2}`)

	if !members[0].filter(frame1) || !members[1].filter(frame2) {
		t.Fatal("distinct frames must both pass")
	}
}

func TestPoolCapacityResetReadmitsHash(t *testing.T) {
	p, _ := newStubPool(1)
	p = NewPool[*stubMember]([]*stubMember{{}}, WithCapacity[*stubMember](2))
	frame := []byte(`{"x":1}`)
	other := []byte(`{"x":2}`)

	p.members[0].filter(frame)
	p.members[0].filter(other)
	// Capacity hit, set resets next insert.
	if !p.members[0].filter([]byte(`{"x":3}`)) {
		t.Fatal("third unique frame should reset and accept")
	}
	// After reset, the original frame is treated as new again.
	if !p.members[0].filter(frame) {
		t.Fatal("after reset, old frame must be accepted again")
	}
}

func TestPoolConnectionTrackingFiresHooks(t *testing.T) {
	var ups, downs atomic.Int64
	pool := NewPool[*stubMember](
		[]*stubMember{{}, {}},
		WithOnFirstUp[*stubMember](func() { ups.Add(1) }),
		WithOnAllDown[*stubMember](func() { downs.Add(1) }),
	)
	if pool.Connected() {
		t.Fatal("Connected must start false")
	}

	pool.members[0].onConnect()
	if !pool.Connected() {
		t.Fatal("Connected must flip true on first up")
	}
	pool.members[1].onConnect() // already up; first-up hook should not fire again
	if ups.Load() != 1 {
		t.Fatalf("onFirstUp fired %d times, want 1", ups.Load())
	}

	pool.members[0].onDisc()
	if !pool.Connected() {
		t.Fatal("Connected should remain true with one member up")
	}
	pool.members[1].onDisc()
	if pool.Connected() {
		t.Fatal("Connected should be false when all members down")
	}
	if downs.Load() != 1 {
		t.Fatalf("onAllDown fired %d times, want 1", downs.Load())
	}
}

func TestPoolConcurrentAcceptUniqueOnce(t *testing.T) {
	p, _ := newStubPool(2)
	const n = 200
	const workers = 4

	frames := make([][]byte, n)
	for i := range frames {
		frames[i] = []byte(fmt.Sprintf(`{"i":%d}`, i))
	}

	var accepted atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, fr := range frames {
				if p.members[0].filter(fr) {
					accepted.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if got := accepted.Load(); got != int64(n) {
		t.Fatalf("accepts=%d, want %d (each unique frame exactly once)", got, n)
	}
}
