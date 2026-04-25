package polymarket

import (
	"sync/atomic"
	"testing"

	"github.com/ntlsxq/polymarket-go/book"
)

const (
	yesTID = "71321045679252212594626385532706912750332728571942532289631379312455583992563"
	noTID  = "10000000000000000000000000000000000000000000000000000000000000000000000000001"
)

func newTestWS(t testing.TB) (*MarketWS, *book.Manager) {
	t.Helper()
	mgr := book.NewManager([]book.Token{
		{Key: "k1", ID: yesTID, IsYes: true},
		{Key: "k1", ID: noTID, IsYes: false},
	})
	ws := NewMarketWS(mgr)
	return ws, mgr
}

func TestDispatchBookSnapshot(t *testing.T) {
	ws, mgr := newTestWS(t)
	raw := []byte(`{
		"event_type":"book",
		"asset_id":"` + yesTID + `",
		"bids":[{"price":"0.50","size":"5"},{"price":"0.49","size":"8"}],
		"asks":[{"price":"0.55","size":"3"},{"price":"0.56","size":"10"}]
	}`)

	ws.dispatch(raw)

	ob := mgr.OBForToken(yesTID)
	bp, _, ok := ob.BestBid()
	if !ok || bp != 0.50 {
		t.Fatalf("BestBid=%v ok=%v, want 0.50", bp, ok)
	}
	ap, _, ok := ob.BestAsk()
	if !ok || ap != 0.55 {
		t.Fatalf("BestAsk=%v ok=%v, want 0.55", ap, ok)
	}
}

func TestDispatchPriceChangeArray(t *testing.T) {
	ws, mgr := newTestWS(t)
	raw := []byte(`[{
		"event_type":"price_change",
		"price_changes":[
			{"asset_id":"` + yesTID + `","side":"BUY","price":"0.49","size":"7"},
			{"asset_id":"` + yesTID + `","side":"SELL","price":"0.56","size":"4"}
		]
	}]`)

	ws.dispatch(raw)

	ob := mgr.OBForToken(yesTID)
	bids := ob.BidLevels()
	if len(bids) != 1 || bids[0].Price != 0.49 || bids[0].Size != 7 {
		t.Fatalf("bids=%+v", bids)
	}
	asks := ob.AskLevels()
	if len(asks) != 1 || asks[0].Price != 0.56 || asks[0].Size != 4 {
		t.Fatalf("asks=%+v", asks)
	}
}

func TestDispatchBestBidAskReconciles(t *testing.T) {
	ws, mgr := newTestWS(t)
	ob := mgr.OBForToken(yesTID)
	ob.SetFromSnapshot(
		[]book.BookLevel{{Price: 0.50, Size: 5}, {Price: 0.51, Size: 3}},
		[]book.BookLevel{{Price: 0.55, Size: 3}, {Price: 0.54, Size: 5}},
	)

	raw := []byte(`{
		"event_type":"best_bid_ask",
		"asset_id":"` + yesTID + `",
		"best_bid":"0.50",
		"best_ask":"0.55"
	}`)
	ws.dispatch(raw)

	for _, b := range ob.BidLevels() {
		if b.Price > 0.50+1e-9 {
			t.Fatalf("bid above bestBid not trimmed: %+v", b)
		}
	}
	for _, a := range ob.AskLevels() {
		if a.Price < 0.55-1e-9 {
			t.Fatalf("ask below bestAsk not trimmed: %+v", a)
		}
	}
}

func TestDispatchLastTradePriceAppliesAndDedupes(t *testing.T) {
	ws, mgr := newTestWS(t)
	ob := mgr.OBForToken(yesTID)
	ob.SetFromSnapshot(nil, []book.BookLevel{{Price: 0.55, Size: 5}})

	raw := []byte(`{
		"event_type":"last_trade_price",
		"asset_id":"` + yesTID + `",
		"side":"BUY",
		"price":"0.55",
		"size":"2",
		"transaction_hash":"0xfeed"
	}`)
	ws.dispatch(raw)
	ws.dispatch(raw) // replay must dedup

	asks := ob.AskLevels()
	if len(asks) != 1 || asks[0].Size != 3 {
		t.Fatalf("expected one decrement, got %+v", asks)
	}
}

func TestDispatchTickSizeChangeUpdatesAndCallsHook(t *testing.T) {
	ws, mgr := newTestWS(t)

	var hookTok string
	var hookSize string
	ws.onTickSizeChange = func(tok, sz string) {
		hookTok = tok
		hookSize = sz
	}

	raw := []byte(`{
		"event_type":"tick_size_change",
		"asset_id":"` + yesTID + `",
		"tick_size":"0.001",
		"minimum_tick_size":"0.001"
	}`)
	ws.dispatch(raw)

	if got := mgr.GetTickSize(yesTID); got != "0.001" {
		t.Fatalf("manager tick = %q, want 0.001", got)
	}
	if hookTok != yesTID || hookSize != "0.001" {
		t.Fatalf("hook not called correctly: tok=%q size=%q", hookTok, hookSize)
	}
}

func TestDispatchUnknownTokenIsNoop(t *testing.T) {
	ws, _ := newTestWS(t)
	raw := []byte(`{
		"event_type":"price_change",
		"price_changes":[{"asset_id":"GHOST","side":"BUY","price":"0.49","size":"7"}]
	}`)
	// Should not panic and should not bump priceChange counter.
	var fired atomic.Bool
	ws.onPriceChange = func() { fired.Store(true) }
	ws.dispatch(raw)
	if fired.Load() {
		t.Fatal("price-change hook fired for unknown token")
	}
}

func TestDispatchOnPriceChangeFiresExactlyOnce(t *testing.T) {
	ws, _ := newTestWS(t)
	var n atomic.Int64
	ws.onPriceChange = func() { n.Add(1) }

	raw := []byte(`[
		{"event_type":"price_change","price_changes":[
			{"asset_id":"` + yesTID + `","side":"BUY","price":"0.49","size":"7"},
			{"asset_id":"` + yesTID + `","side":"SELL","price":"0.56","size":"4"}
		]},
		{"event_type":"price_change","price_changes":[
			{"asset_id":"` + yesTID + `","side":"BUY","price":"0.48","size":"1"}
		]}
	]`)
	ws.dispatch(raw)

	if got := n.Load(); got != 1 {
		t.Fatalf("onPriceChange fired %d times, want 1 per dispatch", got)
	}
}

func TestDispatchFilterDropsFrame(t *testing.T) {
	ws, mgr := newTestWS(t)
	ws.SetFilter(func(_ []byte) bool { return false })
	raw := []byte(`{
		"event_type":"book",
		"asset_id":"` + yesTID + `",
		"bids":[{"price":"0.50","size":"5"}],
		"asks":[]
	}`)
	ws.dispatch(raw)

	ob := mgr.OBForToken(yesTID)
	if _, _, ok := ob.BestBid(); ok {
		t.Fatal("filter must drop the frame so book stays empty")
	}
}

func TestParseLastTradePriceHandlesEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		msg  wsLastTradePriceMsg
		want bool
	}{
		{"happy", wsLastTradePriceMsg{AssetID: yesTID, Side: "BUY", Price: "0.55", Size: "2", TransactionHash: "0xfeed"}, true},
		{"bad side", wsLastTradePriceMsg{AssetID: yesTID, Side: "WAT", Price: "0.55", Size: "2"}, false},
		{"zero price", wsLastTradePriceMsg{AssetID: yesTID, Side: "BUY", Price: "0", Size: "2"}, false},
		{"zero size", wsLastTradePriceMsg{AssetID: yesTID, Side: "BUY", Price: "0.55", Size: "0"}, false},
		{"non-numeric price", wsLastTradePriceMsg{AssetID: yesTID, Side: "BUY", Price: "abc", Size: "2"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ok := parseLastTradePrice(tc.msg)
			if ok != tc.want {
				t.Fatalf("parseLastTradePrice ok=%v, want %v", ok, tc.want)
			}
		})
	}
}

func TestDispatchSingleObjectFallback(t *testing.T) {
	ws, mgr := newTestWS(t)
	// Single object (no wrapping array) — dispatch must handle the
	// failed-array unmarshal fallback path.
	raw := []byte(`{
		"event_type":"book",
		"asset_id":"` + yesTID + `",
		"bids":[{"price":"0.50","size":"5"}],
		"asks":[{"price":"0.55","size":"3"}]
	}`)
	ws.dispatch(raw)

	ob := mgr.OBForToken(yesTID)
	if _, _, ok := ob.BestBid(); !ok {
		t.Fatal("single-object fallback did not apply book")
	}
}
