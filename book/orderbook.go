package book

import (
	"math"
	"sort"
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

type atomicBest struct {
	price atomic.Int32
	size  atomic.Uint64
	has   atomic.Bool
}

func (a *atomicBest) store(price int32, size float64) {
	a.price.Store(price)
	a.size.Store(math.Float64bits(size))
	a.has.Store(true)
}

func (a *atomicBest) clear() {
	a.has.Store(false)
	a.price.Store(0)
	a.size.Store(0)
}

func (a *atomicBest) load() (price float64, size float64, ok bool) {
	if !a.has.Load() {
		return 0, 0, false
	}
	return ToFloat(a.price.Load()), math.Float64frombits(a.size.Load()), true
}

type OrderBook struct {
	mu sync.RWMutex

	bids map[int32]float64
	asks map[int32]float64

	bidBest       int32
	askBest       int32
	hasBids       bool
	hasAsks       bool
	sortedAsks    []int32
	sortedBids    []int32
	totalAskDepth float64

	// bidLevels / askLevels cache the BookLevel views handed out by
	// BidLevels() / AskLevels(). Rebuilt under write-lock by recompute*;
	// readers under RLock just return the slice header. Callers must
	// treat them as immutable — they're shared. This is what turns 4M
	// per-tick BidLevels() calls from "sort + alloc + copy" into a pointer
	// hand-off (the sort dominated CPU per pprof).
	bidLevels []BookLevel
	askLevels []BookLevel

	atomicBid atomicBest
	atomicAsk atomicBest

	// version increments on every mutation that can change MaxBuy/ceilPrice
	// inputs (levels, best bid/ask, reconcile, clear). Consumers (MM) poll
	// it to detect dirty books without needing a push channel from WS.
	version atomic.Uint64
}

func (ob *OrderBook) Version() uint64 { return ob.version.Load() }

func NewOrderBook() *OrderBook {
	return &OrderBook{
		bids: make(map[int32]float64),
		asks: make(map[int32]float64),
	}
}

func (ob *OrderBook) ClearAtomics() {
	ob.atomicBid.clear()
	ob.atomicAsk.clear()
	ob.version.Add(1)
}

func (ob *OrderBook) BestBid() (price float64, size float64, ok bool) {
	return ob.atomicBid.load()
}

func (ob *OrderBook) BestAsk() (price float64, size float64, ok bool) {
	return ob.atomicAsk.load()
}

func (ob *OrderBook) TotalAskDepth() float64 {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return ob.totalAskDepth
}

func (ob *OrderBook) CostToBuy(shares float64) (cost float64, filled float64) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	remaining := shares
	for _, pk := range ob.sortedAsks {
		sz := ob.asks[pk]
		take := remaining
		if sz < take {
			take = sz
		}
		cost += take * ToFloat(pk)
		remaining -= take
		if remaining <= 0 {
			break
		}
	}
	return cost, shares - remaining
}

func (ob *OrderBook) WorstAskForSize(shares float64) (price float64, filled float64) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	remaining := shares
	var lastPrice float64
	for _, pk := range ob.sortedAsks {
		sz := ob.asks[pk]
		lastPrice = ToFloat(pk)
		take := remaining
		if sz < take {
			take = sz
		}
		remaining -= take
		if remaining <= 0 {
			break
		}
	}
	return lastPrice, shares - remaining
}

func (ob *OrderBook) WorstBidForSize(shares float64) (price float64, filled float64) {
	ob.mu.RLock()
	defer ob.mu.RUnlock()

	remaining := shares
	var lastPrice float64
	for _, pk := range ob.sortedBids {
		sz := ob.bids[pk]
		lastPrice = ToFloat(pk)
		take := remaining
		if sz < take {
			take = sz
		}
		remaining -= take
		if remaining <= 0 {
			break
		}
	}
	return lastPrice, shares - remaining
}

// AskLevels returns ascending ask levels. The slice is cached and shared
// across callers; treat as read-only — mutating it corrupts the OrderBook.
func (ob *OrderBook) AskLevels() []BookLevel {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return ob.askLevels
}

// BidLevels returns descending bid levels. The slice is cached and shared
// across callers; treat as read-only — mutating it corrupts the OrderBook.
func (ob *OrderBook) BidLevels() []BookLevel {
	ob.mu.RLock()
	defer ob.mu.RUnlock()
	return ob.bidLevels
}

func (ob *OrderBook) UpdateLevel(side Side, price float64, size float64) {
	pk := ToInt(price)

	ob.mu.Lock()
	defer ob.mu.Unlock()

	if side == SideBuy {
		if size <= 0 {
			delete(ob.bids, pk)
		} else {
			ob.bids[pk] = size
		}
		ob.recomputeBids()
	} else {
		if size <= 0 {
			delete(ob.asks, pk)
		} else {
			ob.asks[pk] = size
		}
		ob.recomputeAsks()
	}
	ob.version.Add(1)
}

// ApplyTrade decrements a single level by the traded size, simulating the
// consumption that a trade event reports. side is the TAKER side: SideBuy
// consumes asks at price, SideSell consumes bids. No-op if the level is
// absent from our local snapshot (the subsequent price_change / book diff
// will reconcile). Bumps version so MM's dirty-tracking picks up the
// mutation.
//
// Used to front-run price_change by seconds: last_trade_price arrives from
// the CLOB immediately on match, price_change follows with a p50 ≈ 74ms,
// p95 ≈ 3s lag for the same underlying event.
func (ob *OrderBook) ApplyTrade(side Side, price, size float64) {
	if price <= 0 || size <= 0 {
		return
	}
	pk := ToInt(price)

	ob.mu.Lock()
	defer ob.mu.Unlock()

	switch side {
	case SideBuy:
		cur, ok := ob.asks[pk]
		if !ok {
			return
		}
		if remaining := cur - size; remaining <= 0 {
			delete(ob.asks, pk)
		} else {
			ob.asks[pk] = remaining
		}
		ob.recomputeAsks()
	case SideSell:
		cur, ok := ob.bids[pk]
		if !ok {
			return
		}
		if remaining := cur - size; remaining <= 0 {
			delete(ob.bids, pk)
		} else {
			ob.bids[pk] = remaining
		}
		ob.recomputeBids()
	default:
		return
	}
	ob.version.Add(1)
}

func (ob *OrderBook) ConsumeAsks(shares float64) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	remaining := shares
	for _, pk := range ob.sortedAsks {
		sz := ob.asks[pk]
		take := remaining
		if sz < take {
			take = sz
		}
		ob.asks[pk] -= take
		if ob.asks[pk] <= 0 {
			delete(ob.asks, pk)
		}
		remaining -= take
		if remaining <= 0 {
			break
		}
	}
	ob.recomputeAsks()
	ob.version.Add(1)
}

func (ob *OrderBook) ReconcileTop(bestBid, bestAsk float64) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	if bestBid <= 0 {
		clear(ob.bids)
	} else {
		bbKey := ToInt(bestBid)
		for pk := range ob.bids {
			if pk > bbKey {
				delete(ob.bids, pk)
			}
		}
	}
	if bestAsk <= 0 {
		clear(ob.asks)
	} else {
		baKey := ToInt(bestAsk)
		for pk := range ob.asks {
			if pk < baKey {
				delete(ob.asks, pk)
			}
		}
	}
	ob.recomputeBids()
	ob.recomputeAsks()
	ob.version.Add(1)
}

func (ob *OrderBook) SetFromSnapshot(bids, asks []BookLevel) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	clear(ob.bids)
	clear(ob.asks)

	for _, b := range bids {
		if b.Size > 0 {
			ob.bids[ToInt(b.Price)] = b.Size
		}
	}
	for _, a := range asks {
		if a.Size > 0 {
			ob.asks[ToInt(a.Price)] = a.Size
		}
	}

	ob.recomputeBids()
	ob.recomputeAsks()
	ob.version.Add(1)
}

func (ob *OrderBook) recomputeBids() {
	n := len(ob.bids)
	if n == 0 {
		ob.hasBids = false
		ob.bidBest = 0
		ob.sortedBids = ob.sortedBids[:0]
		ob.bidLevels = ob.bidLevels[:0]
		ob.atomicBid.clear()
		return
	}
	ob.hasBids = true

	if cap(ob.sortedBids) >= n {
		ob.sortedBids = ob.sortedBids[:0]
	} else {
		ob.sortedBids = make([]int32, 0, n)
	}
	for pk := range ob.bids {
		ob.sortedBids = append(ob.sortedBids, pk)
	}
	sort.Slice(ob.sortedBids, func(i, j int) bool {
		return ob.sortedBids[i] > ob.sortedBids[j]
	})

	if cap(ob.bidLevels) >= n {
		ob.bidLevels = ob.bidLevels[:n]
	} else {
		ob.bidLevels = make([]BookLevel, n)
	}
	for i, pk := range ob.sortedBids {
		ob.bidLevels[i] = BookLevel{Price: ToFloat(pk), Size: ob.bids[pk]}
	}

	ob.bidBest = ob.sortedBids[0]
	ob.atomicBid.store(ob.bidBest, ob.bids[ob.bidBest])
}

func (ob *OrderBook) recomputeAsks() {
	n := len(ob.asks)
	if n == 0 {
		ob.hasAsks = false
		ob.askBest = 0
		ob.sortedAsks = ob.sortedAsks[:0]
		ob.askLevels = ob.askLevels[:0]
		ob.totalAskDepth = 0
		ob.atomicAsk.clear()
		return
	}

	ob.hasAsks = true

	if cap(ob.sortedAsks) >= n {
		ob.sortedAsks = ob.sortedAsks[:0]
	} else {
		ob.sortedAsks = make([]int32, 0, n)
	}
	var total float64
	for pk, sz := range ob.asks {
		ob.sortedAsks = append(ob.sortedAsks, pk)
		total += sz
	}
	sort.Slice(ob.sortedAsks, func(i, j int) bool {
		return ob.sortedAsks[i] < ob.sortedAsks[j]
	})

	if cap(ob.askLevels) >= n {
		ob.askLevels = ob.askLevels[:n]
	} else {
		ob.askLevels = make([]BookLevel, n)
	}
	for i, pk := range ob.sortedAsks {
		ob.askLevels[i] = BookLevel{Price: ToFloat(pk), Size: ob.asks[pk]}
	}

	ob.askBest = ob.sortedAsks[0]
	ob.totalAskDepth = total
	ob.atomicAsk.store(ob.askBest, ob.asks[ob.askBest])
}
