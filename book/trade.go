package book

import (
	"hash/maphash"
	"sync"
)

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

const (
	defaultSeenTradesCapacity = 4096
	dedupShardCount           = 16
	dedupShardMask            = dedupShardCount - 1
)

// tradeDedup is a sharded set of transaction hashes. N=16 shards, each with
// its own mutex, drops mutex contention by ~16× compared to a single-lock
// map under fan-in from multiple WS goroutines. Reset-on-full keeps memory
// capped per-shard with no TTL machinery.
type tradeDedup struct {
	seed      maphash.Seed
	shards    [dedupShardCount]dedupShard
	shardCap  int
}

type dedupShard struct {
	mu   sync.Mutex
	seen map[uint64]struct{}
	// Pad to fill a 64-byte cache line and avoid false sharing between
	// adjacent shards. sync.Mutex is 8 bytes, map header 8 bytes; pad to 64.
	_ [48]byte
}

func newTradeDedup(capacity int) *tradeDedup {
	if capacity <= 0 {
		capacity = defaultSeenTradesCapacity
	}
	per := capacity / dedupShardCount
	if per < 1 {
		per = 1
	}
	d := &tradeDedup{
		seed:     maphash.MakeSeed(),
		shardCap: per,
	}
	for i := range d.shards {
		d.shards[i].seen = make(map[uint64]struct{}, per)
	}
	return d
}

// capacity returns the configured per-shard capacity, kept for tests that
// want to assert the default-sizing behavior.
func (d *tradeDedup) capacity() int { return d.shardCap * dedupShardCount }

// markSeen returns true the first time hash is observed and false on
// repeats. Empty hash is treated as unique (returns true) — the caller
// decides whether to still apply the mutation.
func (d *tradeDedup) markSeen(hash string) bool {
	if hash == "" {
		return true
	}
	k := maphash.String(d.seed, hash)
	sh := &d.shards[k&dedupShardMask]
	sh.mu.Lock()
	if _, ok := sh.seen[k]; ok {
		sh.mu.Unlock()
		return false
	}
	if len(sh.seen) >= d.shardCap {
		sh.seen = make(map[uint64]struct{}, d.shardCap)
	}
	sh.seen[k] = struct{}{}
	sh.mu.Unlock()
	return true
}
