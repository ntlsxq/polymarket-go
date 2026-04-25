package polymarket

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ntlsxq/polymarket-go/book"
)

// freshDispatcher rebuilds a ready MarketWS+Manager pair so each bench is
// isolated.
func freshDispatcher() (*MarketWS, *book.Manager) {
	mgr := book.NewManager([]book.Token{
		{Key: "k1", ID: yesTID, IsYes: true},
		{Key: "k1", ID: noTID, IsYes: false},
	})
	return NewMarketWS(mgr), mgr
}

func priceChangeArrayFrame(n int) []byte {
	var b strings.Builder
	b.WriteString(`[{"event_type":"price_change","price_changes":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		side := "BUY"
		if i%2 == 1 {
			side = "SELL"
		}
		fmt.Fprintf(&b, `{"asset_id":"%s","side":"%s","price":"0.%04d","size":"%d"}`,
			yesTID, side, 4900+i%50, 1+i%50)
	}
	b.WriteString(`]}]`)
	return []byte(b.String())
}

func bookSnapshotFrame(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"event_type":"book","asset_id":"`)
	b.WriteString(yesTID)
	b.WriteString(`","bids":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"price":"0.%04d","size":"%d"}`, 5000-(i+1), 1+i)
	}
	b.WriteString(`],"asks":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"price":"0.%04d","size":"%d"}`, 5100+i, 1+i)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func bestBidAskFrame() []byte {
	return []byte(`{"event_type":"best_bid_ask","asset_id":"` + yesTID + `","best_bid":"0.50","best_ask":"0.55"}`)
}

func lastTradeFrame(seq int) []byte {
	return []byte(fmt.Sprintf(
		`{"event_type":"last_trade_price","asset_id":"%s","side":"BUY","price":"0.5100","size":"1","transaction_hash":"0x%016x"}`,
		yesTID, seq))
}

// BenchmarkDispatchPriceChange measures the typical hot-path event:
// price_change with a few entries packed into an array frame.
func BenchmarkDispatchPriceChange(b *testing.B) {
	for _, n := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			ws, _ := freshDispatcher()
			raw := priceChangeArrayFrame(n)
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ws.dispatch(raw)
			}
		})
	}
}

// BenchmarkDispatchBookSnapshot measures the cost of a "book" event — full
// SetFromSnapshot. n is per-side level count.
func BenchmarkDispatchBookSnapshot(b *testing.B) {
	for _, n := range []int{10, 50} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ws, _ := freshDispatcher()
			raw := bookSnapshotFrame(n)
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ws.dispatch(raw)
			}
		})
	}
}

// BenchmarkDispatchBestBidAsk is the cheapest event type (single reconcile).
// Useful as a baseline for envelope-parsing overhead.
func BenchmarkDispatchBestBidAsk(b *testing.B) {
	ws, mgr := freshDispatcher()
	mgr.OBForToken(yesTID).SetFromSnapshot(
		[]book.BookLevel{{Price: 0.50, Size: 5}, {Price: 0.51, Size: 3}},
		[]book.BookLevel{{Price: 0.55, Size: 3}, {Price: 0.54, Size: 5}},
	)
	raw := bestBidAskFrame()
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ws.dispatch(raw)
	}
}

// BenchmarkDispatchLastTradePrice issues unique tx-hashes so dedup always
// passes — mirrors steady-state trade frequency.
func BenchmarkDispatchLastTradePrice(b *testing.B) {
	ws, mgr := freshDispatcher()
	mgr.OBForToken(yesTID).SetFromSnapshot(nil,
		[]book.BookLevel{{Price: 0.51, Size: float64(b.N + 1_000_000)}})
	frames := make([][]byte, 1024)
	for i := range frames {
		frames[i] = lastTradeFrame(i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ws.dispatch(frames[i%len(frames)])
	}
}

// BenchmarkDispatchMixedRealistic alternates the dominant frame types in the
// approximate ratio observed on prod (price_change ≫ best_bid_ask > trade ≫ book).
func BenchmarkDispatchMixedRealistic(b *testing.B) {
	ws, mgr := freshDispatcher()
	mgr.OBForToken(yesTID).SetFromSnapshot(
		[]book.BookLevel{{Price: 0.50, Size: 10}, {Price: 0.49, Size: 20}, {Price: 0.48, Size: 30}},
		[]book.BookLevel{{Price: 0.51, Size: 10}, {Price: 0.52, Size: 20}, {Price: 0.53, Size: 30}},
	)
	pcSmall := priceChangeArrayFrame(2)
	bba := bestBidAskFrame()
	tradeFrames := make([][]byte, 256)
	for i := range tradeFrames {
		tradeFrames[i] = lastTradeFrame(i)
	}
	bookFrame := bookSnapshotFrame(20)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		switch i % 100 {
		case 0:
			ws.dispatch(bookFrame)
		case 1, 2, 3:
			ws.dispatch(tradeFrames[i%len(tradeFrames)])
		case 4, 5, 6, 7, 8, 9:
			ws.dispatch(bba)
		default:
			ws.dispatch(pcSmall)
		}
	}
}
