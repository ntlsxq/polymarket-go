package polymarket

import "testing"

func TestUserWSDispatchTradeAndOrderEvents(t *testing.T) {
	var fills []Fill
	var orders []OrderEvent

	ws := NewUserWS(nil, nil, func(fill Fill) {
		fills = append(fills, fill)
	})
	ws.SetOnOrder(func(order OrderEvent) {
		orders = append(orders, order)
	})

	ws.dispatchRaw([]byte(`{
		"event_type":"trade",
		"id":"F1",
		"status":"MATCHED",
		"asset_id":"T1",
		"side":"BUY",
		"price":"0.50",
		"size":"2",
		"transaction_hash":"0xabc"
	}`))
	ws.dispatchRaw([]byte(`{
		"event_type":"order",
		"id":"O1",
		"asset_id":"T1",
		"side":"BUY",
		"type":"PLACEMENT",
		"original_size":"10",
		"size_matched":"0",
		"price":"0.50"
	}`))

	if len(fills) != 1 || fills[0].ID != "F1" || fills[0].Status != FillStatusMatched {
		t.Fatalf("fills=%+v", fills)
	}
	if len(orders) != 1 || orders[0].ID != "O1" || orders[0].Type != OrderEventPlacement {
		t.Fatalf("orders=%+v", orders)
	}
}

func TestUserWSDispatchDropsNonMatchedTrade(t *testing.T) {
	var fills []Fill
	ws := NewUserWS(nil, nil, func(fill Fill) {
		fills = append(fills, fill)
	})

	ws.dispatchRaw([]byte(`{
		"event_type":"trade",
		"id":"F1",
		"status":"CONFIRMED",
		"asset_id":"T1"
	}`))

	if len(fills) != 0 {
		t.Fatalf("non-MATCHED trade should not call fill handler: %+v", fills)
	}
}
