package polymarket

import (
	"fmt"
	"strings"
	"testing"
)

func freshDispatcher() *MarketWS {
	return NewMarketWS([]string{yesTID, noTID}, WithOnMarketEvent(func(MarketWSEvent) {}))
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

func BenchmarkDispatchPriceChange(b *testing.B) {
	for _, n := range []int{1, 4, 16} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			ws := freshDispatcher()
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

func BenchmarkDispatchBookSnapshot(b *testing.B) {
	for _, n := range []int{10, 50} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			ws := freshDispatcher()
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

func BenchmarkDispatchBestBidAsk(b *testing.B) {
	ws := freshDispatcher()
	raw := bestBidAskFrame()
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ws.dispatch(raw)
	}
}

func BenchmarkDispatchLastTradePrice(b *testing.B) {
	ws := freshDispatcher()
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

func BenchmarkDispatchMixedRealistic(b *testing.B) {
	ws := freshDispatcher()
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
