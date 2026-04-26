package clob

import (
	"testing"

	"github.com/goccy/go-json"
)

func TestOrderUnmarshal(t *testing.T) {
	raw := []byte(`{
		"id": "0xabc",
		"status": "LIVE",
		"owner": "d5b8f...-uuid",
		"maker_address": "0x1111111111111111111111111111111111111111",
		"market": "0xcccc",
		"asset_id": "71321045679252212594626385532706912750332728571942532289631379312455583992563",
		"side": "BUY",
		"original_size": "100",
		"size_matched": "0",
		"price": "0.42",
		"outcome": "Yes",
		"expiration": "0",
		"order_type": "GTC",
		"associate_trades": ["t1","t2"],
		"created_at": 1712000000
	}`)

	var o Order
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if o.ID != "0xabc" || o.Status != OrderStatusLive || o.Side != SideBuy || o.OrderType != OrderTypeGTC {
		t.Fatalf("unexpected fields: %+v", o)
	}
	if len(o.AssociateTrades) != 2 || o.CreatedAt != 1712000000 {
		t.Fatalf("expected 2 trades and created_at=1712000000, got %+v", o)
	}
}

func TestPostOrderResponseUnmarshal(t *testing.T) {
	raw := []byte(`{
		"success": true,
		"orderID": "0xoid",
		"status": "MATCHED",
		"makingAmount": "5000000",
		"takingAmount": "2500000",
		"transactionsHashes": ["0xtxhash1"],
		"tradeIDs": ["tid1"],
		"errorMsg": ""
	}`)

	var r PostOrderResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !r.Success || r.OrderID != "0xoid" || r.Status != OrderStatusMatched {
		t.Fatalf("bad fields: %+v", r)
	}
	if len(r.TransactionsHashes) != 1 || len(r.TradeIDs) != 1 {
		t.Fatalf("arrays not parsed: %+v", r)
	}
}

func TestCancelResponseUnmarshal(t *testing.T) {
	t.Run("partial_failure", func(t *testing.T) {
		raw := []byte(`{
			"canceled": ["0xa1", "0xa2"],
			"not_canceled": {"0xb1": "order not found", "0xb2": "owner mismatch"}
		}`)
		var r CancelResponse
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(r.Canceled) != 2 || r.Canceled[0] != "0xa1" {
			t.Fatalf("canceled wrong: %+v", r.Canceled)
		}
		if len(r.NotCanceled) != 2 || r.NotCanceled["0xb1"] != "order not found" {
			t.Fatalf("not_canceled wrong: %+v", r.NotCanceled)
		}
	})
	t.Run("all_success_empty_map", func(t *testing.T) {
		raw := []byte(`{"canceled":["0x1"],"not_canceled":{}}`)
		var r CancelResponse
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(r.Canceled) != 1 || len(r.NotCanceled) != 0 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
	t.Run("nothing_to_cancel", func(t *testing.T) {
		raw := []byte(`{"canceled":[],"not_canceled":{}}`)
		var r CancelResponse
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(r.Canceled) != 0 || len(r.NotCanceled) != 0 {
			t.Fatalf("unexpected: %+v", r)
		}
	})
}

func TestBalanceAllowanceUnmarshalBothShapes(t *testing.T) {
	legacy := []byte(`{"balance":"1500.000000","allowance":"1500.000000"}`)
	var a BalanceAllowance
	if err := json.Unmarshal(legacy, &a); err != nil {
		t.Fatalf("legacy: %v", err)
	}
	if a.Balance != "1500.000000" || a.Allowance != "1500.000000" {
		t.Fatalf("legacy parse: %+v", a)
	}

	modern := []byte(`{"balance":"100.0","allowances":{"0xexchange":"1000","0xneg":"500"}}`)
	var b BalanceAllowance
	if err := json.Unmarshal(modern, &b); err != nil {
		t.Fatalf("modern: %v", err)
	}
	if b.Balance != "100.0" || len(b.Allowances) != 2 {
		t.Fatalf("modern parse: %+v", b)
	}
}

func TestPositionUnmarshalFullSchema(t *testing.T) {
	raw := []byte(`{
		"proxyWallet": "0xabc",
		"asset": "123",
		"conditionId": "0xcond",
		"size": 10.5,
		"avgPrice": 0.42,
		"initialValue": 100.0,
		"currentValue": 120.0,
		"cashPnl": 20.0,
		"percentPnl": 20.0,
		"totalBought": 100.0,
		"realizedPnl": 0.0,
		"percentRealizedPnl": 0.0,
		"curPrice": 0.5,
		"redeemable": false,
		"mergeable": true,
		"title": "will X happen",
		"slug": "x-happens",
		"icon": "https://ic",
		"eventSlug": "ev-x",
		"outcome": "Yes",
		"outcomeIndex": 0,
		"oppositeOutcome": "No",
		"oppositeAsset": "456",
		"endDate": "2026-12-31",
		"negativeRisk": true
	}`)

	var p Position
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.NegativeRisk != true || p.CashPnl != 20.0 || p.OppositeAsset != "456" {
		t.Fatalf("missing fields: %+v", p)
	}
}
