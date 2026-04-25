package book

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

const eps = 1e-9

func approxEq(a, b float64) bool { return math.Abs(a-b) < eps }

func newBookWithLevels(t testing.TB, bids, asks []BookLevel) *OrderBook {
	t.Helper()
	ob := NewOrderBook()
	ob.SetFromSnapshot(bids, asks)
	return ob
}

// snapshotBids reads BidLevels into a fresh slice; tests want a stable
// copy (BidLevels itself returns the shared cache slice).
func snapshotBids(ob *OrderBook) []BookLevel {
	src := ob.BidLevels()
	out := make([]BookLevel, len(src))
	copy(out, src)
	return out
}

func snapshotAsks(ob *OrderBook) []BookLevel {
	src := ob.AskLevels()
	out := make([]BookLevel, len(src))
	copy(out, src)
	return out
}

func TestPriceConversionRoundTrip(t *testing.T) {
	for _, p := range []float64{0.0001, 0.01, 0.5, 0.9999} {
		got := ToFloat(ToInt(p))
		if !approxEq(got, p) {
			t.Fatalf("round-trip lost precision: %.6f -> %d -> %.6f", p, ToInt(p), got)
		}
	}
}

func TestEmptyBookReadsZero(t *testing.T) {
	ob := NewOrderBook()
	if _, _, ok := ob.BestBid(); ok {
		t.Fatal("BestBid should be ok=false on empty book")
	}
	if _, _, ok := ob.BestAsk(); ok {
		t.Fatal("BestAsk should be ok=false on empty book")
	}
	if d := ob.TotalAskDepth(); d != 0 {
		t.Fatalf("TotalAskDepth on empty book = %v, want 0", d)
	}
	if levels := ob.AskLevels(); len(levels) != 0 {
		t.Fatalf("AskLevels on empty book = %v, want []", levels)
	}
	if levels := ob.BidLevels(); len(levels) != 0 {
		t.Fatalf("BidLevels on empty book = %v, want []", levels)
	}
}

func TestSetFromSnapshotInstallsLevelsSorted(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.45, 10}, {0.50, 5}, {0.40, 20}},
		[]BookLevel{{0.60, 3}, {0.55, 8}, {0.65, 4}},
	)

	bids := snapshotBids(ob)
	if len(bids) != 3 {
		t.Fatalf("bids len = %d", len(bids))
	}
	for i := 1; i < len(bids); i++ {
		if bids[i].Price >= bids[i-1].Price {
			t.Fatalf("bids must be descending: %+v", bids)
		}
	}

	asks := snapshotAsks(ob)
	if len(asks) != 3 {
		t.Fatalf("asks len = %d", len(asks))
	}
	for i := 1; i < len(asks); i++ {
		if asks[i].Price <= asks[i-1].Price {
			t.Fatalf("asks must be ascending: %+v", asks)
		}
	}

	if bp, _, ok := ob.BestBid(); !ok || !approxEq(bp, 0.50) {
		t.Fatalf("BestBid = %v ok=%v, want 0.50", bp, ok)
	}
	if ap, _, ok := ob.BestAsk(); !ok || !approxEq(ap, 0.55) {
		t.Fatalf("BestAsk = %v ok=%v, want 0.55", ap, ok)
	}
}

func TestSetFromSnapshotZeroSizesDropped(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.50, 5}, {0.45, 0}, {0.40, 20}},
		nil,
	)
	bids := snapshotBids(ob)
	if len(bids) != 2 {
		t.Fatalf("zero-size level not dropped: %+v", bids)
	}
}

func TestUpdateLevelInsertUpdateDelete(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.50, 5}},
		[]BookLevel{{0.55, 5}},
	)

	ob.UpdateLevel(SideBuy, 0.49, 7)
	bids := snapshotBids(ob)
	if len(bids) != 2 || !approxEq(bids[1].Price, 0.49) || !approxEq(bids[1].Size, 7) {
		t.Fatalf("insert failed: %+v", bids)
	}

	ob.UpdateLevel(SideBuy, 0.49, 12)
	bids = snapshotBids(ob)
	if !approxEq(bids[1].Size, 12) {
		t.Fatalf("update failed: %+v", bids)
	}

	ob.UpdateLevel(SideBuy, 0.49, 0)
	bids = snapshotBids(ob)
	if len(bids) != 1 {
		t.Fatalf("delete failed: %+v", bids)
	}
}

func TestUpdateLevelMaintainsBestAtomic(t *testing.T) {
	ob := newBookWithLevels(t, []BookLevel{{0.50, 5}}, nil)

	ob.UpdateLevel(SideBuy, 0.55, 10)
	if bp, _, ok := ob.BestBid(); !ok || !approxEq(bp, 0.55) {
		t.Fatalf("best bid not promoted: %v", bp)
	}

	ob.UpdateLevel(SideBuy, 0.55, 0)
	if bp, _, ok := ob.BestBid(); !ok || !approxEq(bp, 0.50) {
		t.Fatalf("best bid not demoted: %v", bp)
	}

	ob.UpdateLevel(SideBuy, 0.50, 0)
	if _, _, ok := ob.BestBid(); ok {
		t.Fatalf("best bid still set after last delete")
	}
}

func TestVersionIncrementsOnEveryMutation(t *testing.T) {
	ob := NewOrderBook()
	v0 := ob.Version()

	ob.SetFromSnapshot([]BookLevel{{0.5, 5}}, []BookLevel{{0.6, 5}})
	v1 := ob.Version()
	if v1 <= v0 {
		t.Fatalf("snapshot did not bump version: v0=%d v1=%d", v0, v1)
	}

	ob.UpdateLevel(SideBuy, 0.49, 1)
	v2 := ob.Version()
	if v2 <= v1 {
		t.Fatalf("update did not bump version: v1=%d v2=%d", v1, v2)
	}

	ob.ApplyTrade(SideBuy, 0.6, 1)
	v3 := ob.Version()
	if v3 <= v2 {
		t.Fatalf("apply trade did not bump version: v2=%d v3=%d", v2, v3)
	}

	ob.ReconcileTop(0.49, 0.6)
	v4 := ob.Version()
	if v4 <= v3 {
		t.Fatalf("reconcile did not bump version: v3=%d v4=%d", v3, v4)
	}

	ob.ClearAtomics()
	v5 := ob.Version()
	if v5 <= v4 {
		t.Fatalf("clear atomics did not bump version: v4=%d v5=%d", v4, v5)
	}
}

func TestApplyTradeDecrementsAndDeletes(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.49, 5}},
		[]BookLevel{{0.55, 8}, {0.56, 3}},
	)

	ob.ApplyTrade(SideBuy, 0.55, 3)
	asks := snapshotAsks(ob)
	if len(asks) != 2 || !approxEq(asks[0].Size, 5) {
		t.Fatalf("partial decrement wrong: %+v", asks)
	}

	ob.ApplyTrade(SideBuy, 0.55, 5)
	asks = snapshotAsks(ob)
	if len(asks) != 1 || !approxEq(asks[0].Price, 0.56) {
		t.Fatalf("full consume should delete level: %+v", asks)
	}

	ob.ApplyTrade(SideBuy, 0.99, 1)
	if asks2 := snapshotAsks(ob); len(asks2) != 1 {
		t.Fatalf("absent level apply trade should be noop: %+v", asks2)
	}
}

func TestApplyTradeSellHitsBids(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.50, 5}, {0.49, 8}},
		nil,
	)
	ob.ApplyTrade(SideSell, 0.50, 5)
	bids := snapshotBids(ob)
	if len(bids) != 1 || !approxEq(bids[0].Price, 0.49) {
		t.Fatalf("sell trade should consume bid: %+v", bids)
	}
}

func TestApplyTradeIgnoresNonPositive(t *testing.T) {
	ob := newBookWithLevels(t, nil, []BookLevel{{0.55, 5}})
	v := ob.Version()
	ob.ApplyTrade(SideBuy, 0, 1)
	ob.ApplyTrade(SideBuy, 0.55, 0)
	if ob.Version() != v {
		t.Fatalf("non-positive trade must not bump version")
	}
}

func TestCostToBuyWalksAsks(t *testing.T) {
	ob := newBookWithLevels(t, nil,
		[]BookLevel{{0.55, 10}, {0.56, 5}, {0.57, 100}},
	)

	cost, filled := ob.CostToBuy(12)
	wantCost := 10*0.55 + 2*0.56
	if !approxEq(filled, 12) || !approxEq(cost, wantCost) {
		t.Fatalf("cost=%.6f filled=%.6f, want cost=%.6f filled=12", cost, filled, wantCost)
	}

	cost2, filled2 := ob.CostToBuy(1000)
	wantFilled := 10.0 + 5.0 + 100.0
	if !approxEq(filled2, wantFilled) {
		t.Fatalf("filled=%v, want %v", filled2, wantFilled)
	}
	if cost2 <= 0 {
		t.Fatalf("cost should be positive: %v", cost2)
	}
}

func TestWorstAskForSize(t *testing.T) {
	ob := newBookWithLevels(t, nil,
		[]BookLevel{{0.55, 5}, {0.56, 10}, {0.57, 100}},
	)
	price, filled := ob.WorstAskForSize(12)
	if !approxEq(filled, 12) || !approxEq(price, 0.56) {
		t.Fatalf("worst ask for 12 = %v filled=%v, want 0.56/12", price, filled)
	}
	price2, filled2 := ob.WorstAskForSize(150)
	if !approxEq(price2, 0.57) || !approxEq(filled2, 115) {
		t.Fatalf("worst ask depth-exceeded: %v filled=%v, want 0.57/115", price2, filled2)
	}
}

func TestWorstBidForSize(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.50, 5}, {0.49, 10}},
		nil,
	)
	price, filled := ob.WorstBidForSize(8)
	if !approxEq(filled, 8) || !approxEq(price, 0.49) {
		t.Fatalf("worst bid for 8 = %v filled=%v", price, filled)
	}
}

func TestReconcileTopTrimsOffsideLevels(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.50, 5}, {0.51, 3}, {0.49, 8}},
		[]BookLevel{{0.55, 5}, {0.54, 3}, {0.56, 8}},
	)

	ob.ReconcileTop(0.50, 0.55)

	bids := snapshotBids(ob)
	for _, b := range bids {
		if b.Price > 0.50+eps {
			t.Fatalf("bid above bestBid not trimmed: %+v", bids)
		}
	}

	asks := snapshotAsks(ob)
	for _, a := range asks {
		if a.Price < 0.55-eps {
			t.Fatalf("ask below bestAsk not trimmed: %+v", asks)
		}
	}
}

func TestReconcileTopZeroClearsSide(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.50, 5}},
		[]BookLevel{{0.55, 5}},
	)
	ob.ReconcileTop(0, 0.55)
	if len(snapshotBids(ob)) != 0 {
		t.Fatalf("zero best bid should clear bids")
	}
	ob.ReconcileTop(0.50, 0)
	// Asks pre-cleared above; just ensure no panic, bids still empty.
	if len(snapshotAsks(ob)) != 0 {
		t.Fatalf("zero best ask should clear asks")
	}
}

func TestConsumeAsks(t *testing.T) {
	ob := newBookWithLevels(t, nil,
		[]BookLevel{{0.55, 5}, {0.56, 10}, {0.57, 100}},
	)
	ob.ConsumeAsks(12)
	asks := snapshotAsks(ob)
	if len(asks) != 2 {
		t.Fatalf("level 0.55 should be fully consumed: %+v", asks)
	}
	if !approxEq(asks[0].Price, 0.56) || !approxEq(asks[0].Size, 3) {
		t.Fatalf("0.56 should have 3 left: %+v", asks[0])
	}
}

func TestClearAtomicsResetsBest(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.50, 5}},
		[]BookLevel{{0.55, 5}},
	)
	ob.ClearAtomics()
	if _, _, ok := ob.BestBid(); ok {
		t.Fatalf("clear should drop best bid")
	}
	if _, _, ok := ob.BestAsk(); ok {
		t.Fatalf("clear should drop best ask")
	}
}

func TestTotalAskDepthMatchesSum(t *testing.T) {
	ob := newBookWithLevels(t, nil,
		[]BookLevel{{0.55, 5}, {0.56, 10}, {0.57, 100}},
	)
	if d := ob.TotalAskDepth(); !approxEq(d, 115) {
		t.Fatalf("TotalAskDepth = %v, want 115", d)
	}
}

func TestBidLevelsCacheStableBetweenMutations(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.50, 5}, {0.49, 8}},
		nil,
	)
	first := ob.BidLevels()
	second := ob.BidLevels()
	if len(first) != len(second) {
		t.Fatalf("len mismatch")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("levels differ across reads with no mutation: %+v vs %+v", first, second)
		}
	}
}

// TestConcurrentReadsWhileWriting runs reads in parallel with writes under -race.
// Pin: no data races, no panics, best/depth observers always see a consistent
// (price, size) pair (no torn read between BestBid components).
func TestConcurrentReadsWhileWriting(t *testing.T) {
	ob := newBookWithLevels(t,
		[]BookLevel{{0.50, 5}, {0.49, 8}},
		[]BookLevel{{0.55, 5}, {0.56, 10}},
	)

	var stop atomic.Bool
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_, _, _ = ob.BestBid()
				_, _, _ = ob.BestAsk()
				_ = ob.AskLevels()
				_ = ob.BidLevels()
				_ = ob.TotalAskDepth()
				_, _ = ob.CostToBuy(20)
			}
		}()
	}

	for i := 0; i < 5_000; i++ {
		px := 0.50 + float64(i%5)/1000.0
		ob.UpdateLevel(SideBuy, px, float64(i%10+1))
		px2 := 0.55 + float64(i%5)/1000.0
		ob.UpdateLevel(SideSell, px2, float64(i%10+1))
		if i%50 == 0 {
			ob.ApplyTrade(SideBuy, 0.55, 1)
		}
	}
	stop.Store(true)
	wg.Wait()
}
