package book

import "sync"

// Trade is one book-consuming match. Side is the TAKER side: a BUY trade
// consumes asks at Price, a SELL trade consumes bids. Hash is the on-chain
// transaction_hash, used for cross-stream dedup (same tx reaches us via
// market-ws last_trade_price and via UserWS maker fills). Empty Hash is
// treated as unique — see tradeDedup.markSeen.
type Trade struct {
	Hash  string
	Side  Side
	Price float64
	Size  float64
}

const defaultSeenTradesCapacity = 4096

// tradeDedup is a bounded set of transaction hashes. Reset-on-full keeps
// memory capped with no TTL machinery: at realistic trade rates, capacity
// preserves far more history than the gap between two feeds describing the
// same tx.
type tradeDedup struct {
	mu       sync.Mutex
	seen     map[string]struct{}
	capacity int
}

func newTradeDedup(capacity int) *tradeDedup {
	if capacity <= 0 {
		capacity = defaultSeenTradesCapacity
	}
	return &tradeDedup{
		seen:     make(map[string]struct{}, capacity),
		capacity: capacity,
	}
}

// markSeen returns true the first time hash is observed and false on
// repeats. Empty hash is treated as unique (returns true) — the caller
// decides whether to still apply the mutation.
func (d *tradeDedup) markSeen(hash string) bool {
	if hash == "" {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[hash]; ok {
		return false
	}
	if len(d.seen) >= d.capacity {
		d.seen = make(map[string]struct{}, d.capacity)
	}
	d.seen[hash] = struct{}{}
	return true
}
