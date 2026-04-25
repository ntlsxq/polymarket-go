package book

import (
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"testing"
)

// fillBook seeds bids and asks tightly around 0.50/0.51 with n levels per side.
// Reproducible: rng seed is fixed.
func fillBook(b *testing.B, ob *OrderBook, n int) {
	b.Helper()
	rng := rand.New(rand.NewPCG(42, uint64(n)))
	bids := make([]BookLevel, 0, n)
	asks := make([]BookLevel, 0, n)
	for i := 0; i < n; i++ {
		bids = append(bids, BookLevel{
			Price: 0.50 - float64(i+1)/1e4,
			Size:  1 + rng.Float64()*100,
		})
		asks = append(asks, BookLevel{
			Price: 0.51 + float64(i)/1e4,
			Size:  1 + rng.Float64()*100,
		})
	}
	ob.SetFromSnapshot(bids, asks)
}

var bookSizes = []int{10, 50, 200}

// BenchmarkUpdateLevelInsert measures the cost of adding a brand-new bid
// level outside existing range. Hot on book-build and price walks.
func BenchmarkUpdateLevelInsert(b *testing.B) {
	for _, n := range bookSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ob := NewOrderBook()
			fillBook(b, ob, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				price := 0.30 - float64(i&0xFFF)/1e4
				ob.UpdateLevel(SideBuy, price, 1)
				ob.UpdateLevel(SideBuy, price, 0)
			}
		})
	}
}

// BenchmarkUpdateLevelUpdate is the steady-state case: an existing level
// changes size. By far the most common WS price_change shape.
func BenchmarkUpdateLevelUpdate(b *testing.B) {
	for _, n := range bookSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ob := NewOrderBook()
			fillBook(b, ob, n)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				idx := i % n
				price := 0.50 - float64(idx+1)/1e4
				ob.UpdateLevel(SideBuy, price, float64(1+(i%50)))
			}
		})
	}
}

// BenchmarkUpdateLevelDelete repeatedly removes and re-adds the deepest bid.
// Captures both delete and insert in steady state.
func BenchmarkUpdateLevelDelete(b *testing.B) {
	for _, n := range bookSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ob := NewOrderBook()
			fillBook(b, ob, n)
			deepest := 0.50 - float64(n)/1e4
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ob.UpdateLevel(SideBuy, deepest, 0)
				ob.UpdateLevel(SideBuy, deepest, 5)
			}
		})
	}
}

// BenchmarkApplyTrade decrements the top ask, deletes when empty, refills.
// Mirrors what last_trade_price drives.
func BenchmarkApplyTrade(b *testing.B) {
	for _, n := range bookSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ob := NewOrderBook()
			fillBook(b, ob, n)
			topAskPrice := 0.51
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ob.ApplyTrade(SideBuy, topAskPrice, 0.5)
				if i%200 == 0 {
					// Refill so the level keeps being present.
					ob.UpdateLevel(SideSell, topAskPrice, 50)
				}
			}
		})
	}
}

// BenchmarkSetFromSnapshot is the path WS hits on every "book" event —
// a full replace of state.
func BenchmarkSetFromSnapshot(b *testing.B) {
	for _, n := range bookSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			bids := make([]BookLevel, n)
			asks := make([]BookLevel, n)
			for i := 0; i < n; i++ {
				bids[i] = BookLevel{Price: 0.50 - float64(i+1)/1e4, Size: float64(1 + i)}
				asks[i] = BookLevel{Price: 0.51 + float64(i)/1e4, Size: float64(1 + i)}
			}
			ob := NewOrderBook()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ob.SetFromSnapshot(bids, asks)
			}
		})
	}
}

// BenchmarkBestBid is the lock-free atomic read path. Should be a few ns/op.
func BenchmarkBestBid(b *testing.B) {
	ob := NewOrderBook()
	fillBook(b, ob, 50)
	var sink float64
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, _, _ := ob.BestBid()
		sink += p
	}
	_ = sink
}

// BenchmarkAskLevels measures the read path consumers hit per strategy tick.
// Currently RLock + slice header return.
func BenchmarkAskLevels(b *testing.B) {
	for _, n := range bookSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ob := NewOrderBook()
			fillBook(b, ob, n)
			var sink int
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				lv := ob.AskLevels()
				sink += len(lv)
			}
			_ = sink
		})
	}
}

// BenchmarkCostToBuy walks asks until quantity is satisfied. Hits map lookup
// per level under RLock — pure read path.
func BenchmarkCostToBuy(b *testing.B) {
	for _, n := range bookSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ob := NewOrderBook()
			fillBook(b, ob, n)
			var sink float64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c, _ := ob.CostToBuy(50)
				sink += c
			}
			_ = sink
		})
	}
}

// BenchmarkWorstAskForSize is the price-walk read path that MM uses for
// slippage estimates. Currently RLock'd iteration.
func BenchmarkWorstAskForSize(b *testing.B) {
	for _, n := range bookSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ob := NewOrderBook()
			fillBook(b, ob, n)
			var sink float64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p, _ := ob.WorstAskForSize(50)
				sink += p
			}
			_ = sink
		})
	}
}

// BenchmarkConcurrentReadWrite mixes high-fanout reads with realistic write
// rate. Captures the RWMutex contention readers currently pay during writes.
// p threads do reads; the goroutine driving b.N does writes.
func BenchmarkConcurrentReadWrite(b *testing.B) {
	ob := NewOrderBook()
	fillBook(b, ob, 50)

	var stop atomic.Bool
	readers := 4
	donec := make(chan struct{}, readers)
	for i := 0; i < readers; i++ {
		go func() {
			var sink int
			for !stop.Load() {
				_, _, _ = ob.BestBid()
				lv := ob.AskLevels()
				sink += len(lv)
				_, _ = ob.CostToBuy(20)
			}
			donec <- struct{}{}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % 50
		ob.UpdateLevel(SideBuy, 0.50-float64(idx+1)/1e4, float64(1+(i%50)))
	}
	b.StopTimer()
	stop.Store(true)
	for i := 0; i < readers; i++ {
		<-donec
	}
}

// BenchmarkChurnRealistic mirrors a hot-period steady state: ~95 % updates
// of existing levels, ~5 % inserts at new prices, occasional snapshot replace,
// trades scattered through. n=50 is the realistic Polymarket book size.
func BenchmarkChurnRealistic(b *testing.B) {
	ob := NewOrderBook()
	fillBook(b, ob, 50)
	rng := rand.New(rand.NewPCG(1, 1))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		switch n := rng.IntN(100); {
		case n < 47:
			idx := rng.IntN(50)
			ob.UpdateLevel(SideBuy, 0.50-float64(idx+1)/1e4, 1+rng.Float64()*50)
		case n < 94:
			idx := rng.IntN(50)
			ob.UpdateLevel(SideSell, 0.51+float64(idx)/1e4, 1+rng.Float64()*50)
		case n < 96:
			ob.UpdateLevel(SideBuy, 0.40-rng.Float64()/1e3, 5)
		case n < 98:
			ob.ApplyTrade(SideBuy, 0.51, 0.5)
		default:
			fillBook(b, ob, 50)
		}
	}
}
