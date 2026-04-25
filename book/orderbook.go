package book

import (
	"math"
	"slices"
	"sync"
	"sync/atomic"
)

const PriceMult = 10000

func ToInt(price float64) int32 {
	return int32(math.Round(price * PriceMult))
}

func ToFloat(pk int32) float64 {
	return float64(pk) / PriceMult
}

type BookLevel struct {
	Price float64
	Size  float64
}

// OrderBook keeps bids and asks as immutable sorted snapshots published
// through atomic.Pointer. Readers do a single atomic load and iterate the
// returned slice without locks; writers serialize on mu, build a new slice
// (copy-on-write), and swap the pointer.
//
// Price keys are snapped to the int32 tick grid (ToInt/ToFloat) on every
// store, so binary search on BookLevel.Price uses the canonical value and
// equality is exact.
type OrderBook struct {
	mu sync.Mutex

	bids atomic.Pointer[[]BookLevel]
	asks atomic.Pointer[[]BookLevel]

	// askDepthBits is the float64 sum of ask sizes, stored as raw bits so
	// it can be loaded without a lock alongside the asks snapshot.
	askDepthBits atomic.Uint64

	// version increments on every state change. Consumers (MM) poll it to
	// detect dirty books without needing a push channel from WS.
	version atomic.Uint64
}

func NewOrderBook() *OrderBook {
	ob := &OrderBook{}
	bids := []BookLevel{}
	asks := []BookLevel{}
	ob.bids.Store(&bids)
	ob.asks.Store(&asks)
	return ob
}

func (ob *OrderBook) Version() uint64 { return ob.version.Load() }

func (ob *OrderBook) ClearAtomics() {
	ob.mu.Lock()
	bids := []BookLevel{}
	asks := []BookLevel{}
	ob.bids.Store(&bids)
	ob.asks.Store(&asks)
	ob.askDepthBits.Store(0)
	ob.version.Add(1)
	ob.mu.Unlock()
}

func (ob *OrderBook) BestBid() (price, size float64, ok bool) {
	lv := *ob.bids.Load()
	if len(lv) == 0 {
		return 0, 0, false
	}
	return lv[0].Price, lv[0].Size, true
}

func (ob *OrderBook) BestAsk() (price, size float64, ok bool) {
	lv := *ob.asks.Load()
	if len(lv) == 0 {
		return 0, 0, false
	}
	return lv[0].Price, lv[0].Size, true
}

func (ob *OrderBook) TotalAskDepth() float64 {
	return math.Float64frombits(ob.askDepthBits.Load())
}

// AskLevels returns the immutable ascending ask snapshot. Callers must
// treat it as read-only — mutating it corrupts the OrderBook.
func (ob *OrderBook) AskLevels() []BookLevel { return *ob.asks.Load() }

// BidLevels returns the immutable descending bid snapshot. Callers must
// treat it as read-only — mutating it corrupts the OrderBook.
func (ob *OrderBook) BidLevels() []BookLevel { return *ob.bids.Load() }

func (ob *OrderBook) CostToBuy(shares float64) (cost, filled float64) {
	levels := *ob.asks.Load()
	remaining := shares
	for i := range levels {
		take := remaining
		if levels[i].Size < take {
			take = levels[i].Size
		}
		cost += take * levels[i].Price
		remaining -= take
		if remaining <= 0 {
			break
		}
	}
	return cost, shares - remaining
}

func (ob *OrderBook) WorstAskForSize(shares float64) (price, filled float64) {
	levels := *ob.asks.Load()
	return walkLevels(levels, shares)
}

func (ob *OrderBook) WorstBidForSize(shares float64) (price, filled float64) {
	levels := *ob.bids.Load()
	return walkLevels(levels, shares)
}

func walkLevels(levels []BookLevel, shares float64) (price, filled float64) {
	remaining := shares
	var lastPrice float64
	for i := range levels {
		lastPrice = levels[i].Price
		take := remaining
		if levels[i].Size < take {
			take = levels[i].Size
		}
		remaining -= take
		if remaining <= 0 {
			break
		}
	}
	return lastPrice, shares - remaining
}

// cmpBidsDesc orders bids highest-first. Used by binary search.
func cmpBidsDesc(a, b BookLevel) int {
	if a.Price > b.Price {
		return -1
	}
	if a.Price < b.Price {
		return 1
	}
	return 0
}

// cmpAsksAsc orders asks lowest-first. Used by binary search.
func cmpAsksAsc(a, b BookLevel) int {
	if a.Price < b.Price {
		return -1
	}
	if a.Price > b.Price {
		return 1
	}
	return 0
}

// applyLevelCOW returns a new slice with (price, size) update applied to
// cur. size <= 0 deletes the level. The caller must pass a price already
// snapped to the tick grid via ToFloat(ToInt(.)).
//
// Returns (next, true) when the slice changed; (cur, false) on no-op.
func applyLevelCOW(cur []BookLevel, price, size float64, cmp func(a, b BookLevel) int) ([]BookLevel, bool) {
	target := BookLevel{Price: price}
	i, found := slices.BinarySearchFunc(cur, target, cmp)

	if size <= 0 {
		if !found {
			return cur, false
		}
		next := make([]BookLevel, len(cur)-1)
		copy(next, cur[:i])
		copy(next[i:], cur[i+1:])
		return next, true
	}

	if found {
		next := make([]BookLevel, len(cur))
		copy(next, cur)
		next[i].Size = size
		return next, true
	}

	next := make([]BookLevel, len(cur)+1)
	copy(next, cur[:i])
	next[i] = BookLevel{Price: price, Size: size}
	copy(next[i+1:], cur[i:])
	return next, true
}

func sumSizes(levels []BookLevel) float64 {
	var s float64
	for i := range levels {
		s += levels[i].Size
	}
	return s
}

func (ob *OrderBook) UpdateLevel(side Side, price, size float64) {
	pk := ToInt(price)
	normPrice := ToFloat(pk)

	ob.mu.Lock()
	if side == SideBuy {
		cur := *ob.bids.Load()
		next, _ := applyLevelCOW(cur, normPrice, size, cmpBidsDesc)
		ob.bids.Store(&next)
	} else {
		cur := *ob.asks.Load()
		next, _ := applyLevelCOW(cur, normPrice, size, cmpAsksAsc)
		ob.asks.Store(&next)
		ob.askDepthBits.Store(math.Float64bits(sumSizes(next)))
	}
	ob.version.Add(1)
	ob.mu.Unlock()
}

// ApplyTrade decrements a single level by the traded size, simulating the
// consumption that a trade event reports. side is the TAKER side: SideBuy
// consumes asks at price, SideSell consumes bids. No-op if the level is
// absent from our local snapshot (the subsequent price_change / book diff
// will reconcile). Bumps version only when the book actually mutated.
//
// Used to front-run price_change by seconds: last_trade_price arrives from
// the CLOB immediately on match, price_change follows with a p50 ≈ 74ms,
// p95 ≈ 3s lag for the same underlying event.
func (ob *OrderBook) ApplyTrade(side Side, price, size float64) {
	if price <= 0 || size <= 0 {
		return
	}
	pk := ToInt(price)
	normPrice := ToFloat(pk)

	ob.mu.Lock()
	defer ob.mu.Unlock()

	var cur []BookLevel
	var cmp func(a, b BookLevel) int
	switch side {
	case SideBuy:
		cur = *ob.asks.Load()
		cmp = cmpAsksAsc
	case SideSell:
		cur = *ob.bids.Load()
		cmp = cmpBidsDesc
	default:
		return
	}

	target := BookLevel{Price: normPrice}
	i, found := slices.BinarySearchFunc(cur, target, cmp)
	if !found {
		return
	}

	var next []BookLevel
	if remaining := cur[i].Size - size; remaining <= 0 {
		next = make([]BookLevel, len(cur)-1)
		copy(next, cur[:i])
		copy(next[i:], cur[i+1:])
	} else {
		next = make([]BookLevel, len(cur))
		copy(next, cur)
		next[i].Size = remaining
	}

	if side == SideBuy {
		ob.asks.Store(&next)
		ob.askDepthBits.Store(math.Float64bits(sumSizes(next)))
	} else {
		ob.bids.Store(&next)
	}
	ob.version.Add(1)
}

// ConsumeAsks walks asks lowest-first decrementing each by what we still
// need to fill, until shares are exhausted or the book empties. Bumps
// version once at the end.
func (ob *OrderBook) ConsumeAsks(shares float64) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	cur := *ob.asks.Load()
	if len(cur) == 0 {
		return
	}

	next := make([]BookLevel, 0, len(cur))
	remaining := shares
	for i := range cur {
		if remaining <= 0 {
			next = append(next, cur[i])
			continue
		}
		take := remaining
		if cur[i].Size < take {
			take = cur[i].Size
		}
		left := cur[i].Size - take
		remaining -= take
		if left > 0 {
			next = append(next, BookLevel{Price: cur[i].Price, Size: left})
		}
	}

	ob.asks.Store(&next)
	ob.askDepthBits.Store(math.Float64bits(sumSizes(next)))
	ob.version.Add(1)
}

func (ob *OrderBook) ReconcileTop(bestBid, bestAsk float64) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	if bestBid <= 0 {
		empty := []BookLevel{}
		ob.bids.Store(&empty)
	} else {
		bbKey := ToInt(bestBid)
		cur := *ob.bids.Load()
		// First in-range index: bids are descending, drop levels with key > bbKey
		// from the front.
		start := 0
		for start < len(cur) && ToInt(cur[start].Price) > bbKey {
			start++
		}
		if start > 0 {
			next := make([]BookLevel, len(cur)-start)
			copy(next, cur[start:])
			ob.bids.Store(&next)
		}
	}

	if bestAsk <= 0 {
		empty := []BookLevel{}
		ob.asks.Store(&empty)
		ob.askDepthBits.Store(0)
	} else {
		baKey := ToInt(bestAsk)
		cur := *ob.asks.Load()
		// asks ascending, drop levels with key < baKey from the front.
		start := 0
		for start < len(cur) && ToInt(cur[start].Price) < baKey {
			start++
		}
		if start > 0 {
			next := make([]BookLevel, len(cur)-start)
			copy(next, cur[start:])
			ob.asks.Store(&next)
			ob.askDepthBits.Store(math.Float64bits(sumSizes(next)))
		}
	}

	ob.version.Add(1)
}

func (ob *OrderBook) SetFromSnapshot(bids, asks []BookLevel) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	newBids := buildSorted(bids, true)
	newAsks := buildSorted(asks, false)

	ob.bids.Store(&newBids)
	ob.asks.Store(&newAsks)
	ob.askDepthBits.Store(math.Float64bits(sumSizes(newAsks)))
	ob.version.Add(1)
}

// buildSorted normalizes input into a tick-snapped sorted slice. descending
// flag controls bid (highest-first) vs ask (lowest-first) ordering.
// Drops zero/negative sizes and dedupes by price key (last write wins).
func buildSorted(in []BookLevel, descending bool) []BookLevel {
	if len(in) == 0 {
		return []BookLevel{}
	}
	out := make([]BookLevel, 0, len(in))
	for _, lv := range in {
		if lv.Size <= 0 {
			continue
		}
		out = append(out, BookLevel{Price: ToFloat(ToInt(lv.Price)), Size: lv.Size})
	}
	if descending {
		slices.SortFunc(out, cmpBidsDesc)
	} else {
		slices.SortFunc(out, cmpAsksAsc)
	}
	// Dedup adjacent equal-price entries (last wins).
	if len(out) <= 1 {
		return out
	}
	w := 0
	for r := 1; r < len(out); r++ {
		if out[r].Price == out[w].Price {
			out[w] = out[r]
			continue
		}
		w++
		out[w] = out[r]
	}
	return out[:w+1]
}
