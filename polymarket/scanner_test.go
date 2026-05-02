package polymarket

import (
	"reflect"
	"testing"

	"github.com/goccy/go-json"
)

func TestGammaMarketUnmarshalFlexibleFields(t *testing.T) {
	raw := []byte(`{
		"slug":"bitcoin-above-100k-on-may-3",
		"conditionId":"0xabc",
		"groupItemTitle":"100,000",
		"groupItemThreshold":"100000",
		"feeSchedule":{"rate":"0.072","exponent":1,"takerOnly":true,"rebateRate":0.2},
		"clobTokenIds":"[\"yes\",\"no\"]",
		"outcomePrices":"[\"0.40\",\"0.60\"]",
		"orderPriceMinTickSize":0.001,
		"volumeNum":"12.5",
		"active":true,
		"closed":false,
		"acceptingOrders":true,
		"enableOrderBook":true
	}`)

	var got GammaMarket
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Slug != "bitcoin-above-100k-on-may-3" ||
		got.ConditionID != "0xabc" ||
		got.GroupItemThreshold != 100000 ||
		got.OrderPriceMinTickSize != "0.001" ||
		got.VolumeNum != 12.5 {
		t.Fatalf("GammaMarket decoded incorrectly: %+v", got)
	}
	if got.Active == nil || !*got.Active ||
		got.Closed == nil || *got.Closed ||
		got.AcceptingOrders == nil || !*got.AcceptingOrders ||
		got.EnableOrderBook == nil || !*got.EnableOrderBook {
		t.Fatalf("GammaMarket bool fields decoded incorrectly: %+v", got)
	}
	if got.FeeSchedule == nil ||
		got.FeeSchedule.Rate != "0.072" ||
		got.FeeSchedule.Exponent != "1" ||
		!got.FeeSchedule.TakerOnly ||
		got.FeeSchedule.RebateRate != "0.2" {
		t.Fatalf("GammaMarket fee schedule decoded incorrectly: %+v", got.FeeSchedule)
	}
}

func TestGammaEventUnmarshalFlexibleFee(t *testing.T) {
	raw := []byte(`{
		"slug":"bitcoin-above-on-may-3",
		"negRiskMarketID":"0xmarket",
		"volume24hr":"42.5",
		"feeSchedule":{"feeRate":"0.072"},
		"markets":[{"conditionId":"0xabc"}]
	}`)

	var got GammaEvent
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Slug != "bitcoin-above-on-may-3" ||
		got.NegRiskMarketID != "0xmarket" ||
		got.Volume24hr != 42.5 ||
		got.FeeSchedule == nil ||
		got.FeeSchedule.FeeRate != 0.072 ||
		got.FeeSchedule.Rate != "0.072" ||
		got.FeeSchedule.Exponent != "1" ||
		len(got.Markets) != 1 {
		t.Fatalf("GammaEvent decoded incorrectly: %+v", got)
	}
}

func TestGammaMarketTokenAndPriceLists(t *testing.T) {
	m := GammaMarket{
		ClobTokenIDs:  `["yes","no"]`,
		OutcomePrices: `["0.40","0.60"]`,
	}

	tokens, err := m.ClobTokenIDList()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"yes", "no"}; !reflect.DeepEqual(tokens, want) {
		t.Fatalf("ClobTokenIDList() = %v, want %v", tokens, want)
	}

	prices, err := m.OutcomePriceList()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"0.40", "0.60"}; !reflect.DeepEqual(prices, want) {
		t.Fatalf("OutcomePriceList() = %v, want %v", prices, want)
	}
}
