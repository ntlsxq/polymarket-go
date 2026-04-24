package book

import "sync"

type Token struct {
	Key   string
	ID    string
	IsYes bool
}

type Manager struct {
	mu        sync.RWMutex
	books     map[string]*BookPair
	tidToKey  map[string]string
	tidIsYes  map[string]bool
	tickSizes sync.Map

	// trades dedupes book-mutating trade events by transaction_hash so
	// consumers (last_trade_price from market ws + maker fills from UserWS)
	// can feed the same trade without double-decrementing a level.
	trades *tradeDedup
}

func NewManager(tokens []Token) *Manager {
	m := &Manager{
		books:    make(map[string]*BookPair),
		tidToKey: make(map[string]string, len(tokens)),
		tidIsYes: make(map[string]bool, len(tokens)),
		trades:   newTradeDedup(defaultSeenTradesCapacity),
	}
	for _, t := range tokens {
		if _, ok := m.books[t.Key]; !ok {
			m.books[t.Key] = NewBookPair()
		}
		m.tidToKey[t.ID] = t.Key
		m.tidIsYes[t.ID] = t.IsYes
	}
	return m
}

func (m *Manager) Get(key string) *BookPair {
	m.mu.RLock()
	bp, ok := m.books[key]
	m.mu.RUnlock()
	if ok {
		return bp
	}
	return NewBookPair()
}

func (m *Manager) BookForToken(tokenID string) *BookPair {
	m.mu.RLock()
	key, ok := m.tidToKey[tokenID]
	if !ok {
		m.mu.RUnlock()
		return nil
	}
	bp := m.books[key]
	m.mu.RUnlock()
	return bp
}

func (m *Manager) OBForToken(tokenID string) *OrderBook {
	m.mu.RLock()
	key, ok := m.tidToKey[tokenID]
	if !ok {
		m.mu.RUnlock()
		return nil
	}
	bp := m.books[key]
	isYes := m.tidIsYes[tokenID]
	m.mu.RUnlock()
	return bp.ForToken(isYes)
}

func (m *Manager) AllTokenIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.tidToKey))
	for tid := range m.tidToKey {
		ids = append(ids, tid)
	}
	return ids
}

func (m *Manager) AddMarket(key, yesTID, noTID, tickSize string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.books[key]; exists {
		return false
	}
	m.books[key] = NewBookPair()
	m.tidToKey[yesTID] = key
	m.tidToKey[noTID] = key
	m.tidIsYes[yesTID] = true
	m.tidIsYes[noTID] = false
	m.tickSizes.Store(yesTID, tickSize)
	m.tickSizes.Store(noTID, tickSize)
	return true
}

func (m *Manager) ClearAllAtomics() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, bp := range m.books {
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
