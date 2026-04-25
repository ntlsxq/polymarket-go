package book

import (
	"sync"
	"sync/atomic"
)

type Token struct {
	Key   string
	ID    string
	IsYes bool
}

// managerSnapshot is the immutable view of the market registry. Each
// AddMarket builds a fresh snapshot via copy-on-write and atomically
// publishes it; readers (Get/BookForToken/OBForToken/AllTokenIDs) load
// the pointer once and walk pure maps without locks.
type managerSnapshot struct {
	books    map[string]*BookPair
	tidToKey map[string]string
	tidIsYes map[string]bool
	tokenIDs []string // pre-built for AllTokenIDs to avoid runtime allocation
}

type Manager struct {
	// mu only serializes writers (AddMarket). Readers never take it.
	mu sync.Mutex

	snap atomic.Pointer[managerSnapshot]

	tickSizes sync.Map

	// trades dedupes book-mutating trade events by transaction_hash so
	// consumers (last_trade_price from market ws + maker fills from UserWS)
	// can feed the same trade without double-decrementing a level.
	trades *tradeDedup
}

func NewManager(tokens []Token) *Manager {
	m := &Manager{
		trades: newTradeDedup(defaultSeenTradesCapacity),
	}
	snap := &managerSnapshot{
		books:    make(map[string]*BookPair, len(tokens)),
		tidToKey: make(map[string]string, len(tokens)),
		tidIsYes: make(map[string]bool, len(tokens)),
		tokenIDs: make([]string, 0, len(tokens)),
	}
	for _, t := range tokens {
		if _, ok := snap.books[t.Key]; !ok {
			snap.books[t.Key] = NewBookPair()
		}
		if _, dup := snap.tidToKey[t.ID]; !dup {
			snap.tokenIDs = append(snap.tokenIDs, t.ID)
		}
		snap.tidToKey[t.ID] = t.Key
		snap.tidIsYes[t.ID] = t.IsYes
	}
	m.snap.Store(snap)
	return m
}

func (m *Manager) Get(key string) *BookPair {
	if bp, ok := m.snap.Load().books[key]; ok {
		return bp
	}
	return NewBookPair()
}

func (m *Manager) BookForToken(tokenID string) *BookPair {
	snap := m.snap.Load()
	key, ok := snap.tidToKey[tokenID]
	if !ok {
		return nil
	}
	return snap.books[key]
}

func (m *Manager) OBForToken(tokenID string) *OrderBook {
	snap := m.snap.Load()
	key, ok := snap.tidToKey[tokenID]
	if !ok {
		return nil
	}
	return snap.books[key].ForToken(snap.tidIsYes[tokenID])
}

// AllTokenIDs returns the published token-ID slice. The returned slice is
// shared and immutable — callers must not mutate it.
func (m *Manager) AllTokenIDs() []string {
	return m.snap.Load().tokenIDs
}

func (m *Manager) AddMarket(key, yesTID, noTID, tickSize string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur := m.snap.Load()
	if _, exists := cur.books[key]; exists {
		return false
	}

	next := &managerSnapshot{
		books:    make(map[string]*BookPair, len(cur.books)+1),
		tidToKey: make(map[string]string, len(cur.tidToKey)+2),
		tidIsYes: make(map[string]bool, len(cur.tidIsYes)+2),
		tokenIDs: make([]string, len(cur.tokenIDs), len(cur.tokenIDs)+2),
	}
	for k, v := range cur.books {
		next.books[k] = v
	}
	for k, v := range cur.tidToKey {
		next.tidToKey[k] = v
	}
	for k, v := range cur.tidIsYes {
		next.tidIsYes[k] = v
	}
	copy(next.tokenIDs, cur.tokenIDs)

	next.books[key] = NewBookPair()
	next.tidToKey[yesTID] = key
	next.tidToKey[noTID] = key
	next.tidIsYes[yesTID] = true
	next.tidIsYes[noTID] = false
	next.tokenIDs = append(next.tokenIDs, yesTID, noTID)

	m.snap.Store(next)
	m.tickSizes.Store(yesTID, tickSize)
	m.tickSizes.Store(noTID, tickSize)
	return true
}

func (m *Manager) ClearAllAtomics() {
	for _, bp := range m.snap.Load().books {
		bp.Yes.ClearAtomics()
		bp.No.ClearAtomics()
	}
}

func (m *Manager) SetTickSize(tokenID, tickSize string) {
	m.tickSizes.Store(tokenID, tickSize)
}

func (m *Manager) GetTickSize(tokenID string) string {
	v, ok := m.tickSizes.Load(tokenID)
	if !ok {
		return ""
	}
	return v.(string)
}

// IngestTrade applies t to the book for tokenID, deduping by t.Hash so the
// same on-chain tx reported through two streams only decrements the level
// once. Returns true when the trade resolves to a known book and passes the
// dedup; callers (event dispatchers) use that to decide whether the book
// was dirtied.
func (m *Manager) IngestTrade(tokenID string, t Trade) bool {
	ob := m.OBForToken(tokenID)
	if ob == nil {
		return false
	}
	if !m.trades.markSeen(t.Hash) {
		return false
	}
	ob.ApplyTrade(t.Side, t.Price, t.Size)
	return true
}
