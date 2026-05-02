package polymarket

import "testing"

const (
	yesTID = "71321045679252212594626385532706912750332728571942532289631379312455583992563"
	noTID  = "10000000000000000000000000000000000000000000000000000000000000000000000000001"
)

func newTestWS(t testing.TB) *MarketWS {
	t.Helper()
	return NewMarketWS([]string{yesTID, noTID})
}

func TestDispatchFilterDropsFrame(t *testing.T) {
	ws := newTestWS(t)
	ws.SetFilter(func(_ []byte) bool { return false })

	var got []MarketWSEvent
	ws.SetOnMarketEvent(func(ev MarketWSEvent) {
		got = append(got, ev)
	})
	ws.dispatch([]byte(`{
		"event_type":"book",
		"asset_id":"` + yesTID + `",
		"bids":[{"price":"0.50","size":"5"}],
		"asks":[]
	}`))

	if len(got) != 0 {
		t.Fatalf("filter must drop the frame, got %+v", got)
	}
}

func TestDispatchSingleObjectFallback(t *testing.T) {
	ws := newTestWS(t)
	var got []MarketWSEvent
	ws.SetOnMarketEvent(func(ev MarketWSEvent) {
		got = append(got, ev)
	})

	ws.dispatch([]byte(`{
		"event_type":"book",
		"asset_id":"` + yesTID + `",
		"bids":[{"price":"0.50","size":"5"}],
		"asks":[{"price":"0.55","size":"3"}]
	}`))

	if len(got) != 1 || got[0].Type != MarketWSEventBook || got[0].Book == nil {
		t.Fatalf("single object dispatch = %+v", got)
	}
}

func TestDispatchBookLevelsKeepDecimalStrings(t *testing.T) {
	ws := newTestWS(t)
	var got []MarketWSEvent
	ws.SetOnMarketEvent(func(ev MarketWSEvent) {
		got = append(got, ev)
	})

	ws.dispatch([]byte(`{
		"event_type":"book",
		"asset_id":"` + yesTID + `",
		"bids":[{"price":"0.50","size":"5.00"}],
		"asks":[{"price":0.55,"size":3}]
	}`))

	if len(got) != 1 || got[0].Book == nil {
		t.Fatalf("book dispatch = %+v", got)
	}
	if got[0].Book.Bids[0].Price != "0.50" || got[0].Book.Bids[0].Size != "5.00" {
		t.Fatalf("quoted level changed: %+v", got[0].Book.Bids[0])
	}
	if got[0].Book.Asks[0].Price != "0.55" || got[0].Book.Asks[0].Size != "3" {
		t.Fatalf("numeric level not normalized as strings: %+v", got[0].Book.Asks[0])
	}
}

func TestDispatchPublishesTypedMarketEventsForAllEventTypes(t *testing.T) {
	ws := newTestWS(t)

	var got []MarketWSEvent
	ws.SetOnMarketEvent(func(ev MarketWSEvent) {
		got = append(got, ev)
	})

	raw := []byte(`[
		{
			"event_type":"book",
			"asset_id":"` + yesTID + `",
			"bids":[{"price":"0.50","size":"5"}],
			"asks":[{"price":"0.55","size":"3"}]
		},
		{
			"event_type":"price_change",
			"price_changes":[{"asset_id":"` + yesTID + `","side":"BUY","price":"0.49","size":"7"}]
		},
		{
			"event_type":"best_bid_ask",
			"asset_id":"` + yesTID + `",
			"best_bid":"0.50",
			"best_ask":"0.55"
		},
		{
			"event_type":"last_trade_price",
			"asset_id":"` + yesTID + `",
			"side":"BUY",
			"price":"0.55",
			"size":"2",
			"transaction_hash":"0xabc"
		},
		{
			"event_type":"tick_size_change",
			"asset_id":"` + yesTID + `",
			"old_tick_size":"0.01",
			"new_tick_size":"0.001"
		},
		{
			"event_type":"new_market",
			"condition_id":"COND_NEW",
			"market":"COND_NEW",
			"slug":"bitcoin-above-100000-on-april-30",
			"group_item_title":"100000",
			"line":"100000",
			"order_price_min_tick_size":"0.01",
			"clob_token_ids":["YES_NEW","NO_NEW"]
		},
		{
			"event_type":"market_resolved",
			"condition_id":"COND_NEW",
			"market":"COND_NEW",
			"winning_asset_id":"YES_NEW",
			"winning_outcome":"Yes",
			"asset_ids":["YES_NEW","NO_NEW"]
		}
	]`)

	ws.dispatch(raw)

	want := []MarketWSEventType{
		MarketWSEventBook,
		MarketWSEventPriceChange,
		MarketWSEventBestBidAsk,
		MarketWSEventLastTradePrice,
		MarketWSEventTickSizeChange,
		MarketWSEventNewMarket,
		MarketWSEventResolved,
	}
	if len(got) != len(want) {
		t.Fatalf("typed events=%d want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Type != want[i] {
			t.Fatalf("event[%d]=%s want %s", i, got[i].Type, want[i])
		}
		if len(got[i].Raw) == 0 {
			t.Fatalf("event[%d] raw payload is empty", i)
		}
	}
	if got[0].Book == nil || got[0].Book.AssetID != yesTID || len(got[0].Book.Bids) != 1 || got[0].Book.Bids[0].Price != "0.50" {
		t.Fatalf("book typed payload not populated: %+v", got[0].Book)
	}
	if got[1].PriceChange == nil || len(got[1].PriceChange.Changes) != 1 || got[1].PriceChange.Changes[0].Price != "0.49" {
		t.Fatalf("price_change typed payload not populated: %+v", got[1].PriceChange)
	}
	if got[2].BestBidAsk == nil || got[2].BestBidAsk.BestAsk != "0.55" {
		t.Fatalf("best_bid_ask typed payload not populated: %+v", got[2].BestBidAsk)
	}
	if got[3].LastTradePrice == nil || got[3].LastTradePrice.TransactionHash != "0xabc" {
		t.Fatalf("last_trade_price typed payload not populated: %+v", got[3].LastTradePrice)
	}
	if got[4].TickSizeChange == nil || got[4].TickSizeChange.NewTickSize != "0.001" {
		t.Fatalf("tick_size_change typed payload not populated: %+v", got[4].TickSizeChange)
	}
	if got[5].NewMarket == nil || got[5].NewMarket.ConditionID != "COND_NEW" || len(got[5].NewMarket.ClobTokenIDs) != 2 {
		t.Fatalf("new_market typed payload not populated: %+v", got[5].NewMarket)
	}
	if got[6].Resolved == nil || got[6].Resolved.WinningAssetID != "YES_NEW" || len(got[6].Resolved.AssetIDs) != 2 {
		t.Fatalf("market_resolved typed payload not populated: %+v", got[6].Resolved)
	}
}
