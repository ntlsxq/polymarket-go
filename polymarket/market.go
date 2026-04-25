package polymarket

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Market struct {
	Coin            string  `json:"coin"`
	CoinShort       string  `json:"coinShort"`
	Date            string  `json:"date"`
	EventType       string  `json:"eventType"`
	Title           string  `json:"title"`
	Threshold       int     `json:"threshold"`
	YesTID          string  `json:"yesTid"`
	NoTID           string  `json:"noTid"`
	Mid             float64 `json:"mid"`
	ConditionID     string  `json:"conditionId"`
	TickSize        string  `json:"tickSize"`
	Key             string  `json:"key"`
	NegRiskMarketID string  `json:"negRiskMarketId,omitempty"`
	QuestionIndex   int     `json:"questionIndex,omitempty"`
	FeeRate         float64 `json:"feeRate,omitempty"`
	FeeRateBps      int64   `json:"feeRateBps,omitempty"`
	Volume24h       float64 `json:"volume24h,omitempty"`
	Slug            string  `json:"slug,omitempty"`
}

func NewMarket(
	coin, coinShort, date, eventType, title string,
	threshold int,
	yesTID, noTID string,
	mid float64,
	conditionID, tickSize string,
) Market {
	return Market{
		Coin:        coin,
		CoinShort:   coinShort,
		Date:        date,
		EventType:   eventType,
		Title:       title,
		Threshold:   threshold,
		YesTID:      yesTID,
		NoTID:       noTID,
		Mid:         mid,
		ConditionID: conditionID,
		TickSize:    tickSize,
		Key:         fmt.Sprintf("%s|%s|%s|%d", coin, date, eventType, threshold),
	}
}

func (m *Market) NegRisk() bool { return m.EventType == "range" }

func (m *Market) IsYes(tokenID string) bool { return m.YesTID == tokenID }

func (m *Market) TokenID(side string) string {
	if side == "YES" {
		return m.YesTID
	}
	return m.NoTID
}

const (
	FeeRateDefault = 0.072

	FeeRateNew = FeeRateDefault
)

const feeCacheSize = 10001

type FeeParams struct {
	Rate   float64
	Rebate float64
	cache  *[feeCacheSize]float64
}

func NewFeeParams(rate float64, rebate float64) FeeParams {
	fp := FeeParams{Rate: rate, Rebate: rebate}
	var cache [feeCacheSize]float64
	for i := 0; i < feeCacheSize; i++ {
		p := float64(i) / 10000.0
		cache[i] = fp.FeePerShareWithRate(p, rate)
	}
	fp.cache = &cache
	return fp
}

func (fp FeeParams) FeePerShare(p float64) float64 {
	if fp.cache != nil {
		idx := int(math.Round(p * 10000))
		if idx >= 0 && idx < feeCacheSize {
			return fp.cache[idx]
		}
	}
	return fp.FeePerShareWithRate(p, fp.Rate)
}

// FeePerShareKey is the hot-path variant of FeePerShare for callers that
// already hold the int32 tick key (e.g. anything reading book.OrderBook
// internals). Skips the math.Round + multiplication that FeePerShare needs
// to recover the index from a float64. Returns 0 for out-of-range keys.
func (fp FeeParams) FeePerShareKey(pk int32) float64 {
	if fp.cache != nil && pk >= 0 && int(pk) < feeCacheSize {
		return fp.cache[pk]
	}
	return fp.FeePerShareWithRate(float64(pk)/10000.0, fp.Rate)
}

// FeePerShareWithRate returns the CTFExchange taker fee in USDC per gross
// share bought:
//
//	fee_usdc_per_share = rate × p × (1-p) × (1 - Rebate)
//
// The on-chain protocol takes 10% of shares on the cheap side of the pair;
// in USDC that's symmetric in p, hence rate × p × (1-p). Rate is already
// the post-rebate effective rate (≈ 0.072 empirically); Rebate is a
// further discount knob layered on top.
func (fp FeeParams) FeePerShareWithRate(p, rate float64) float64 {
	if p <= 0 || p >= 1 {
		return 0
	}
	f := rate * p * (1 - p)
	if fp.Rebate > 0 {
		f *= 1 - fp.Rebate
	}
	return f
}

func (m *Market) EffectiveFeeRate() float64 {
	if m.FeeRate > 0 {
		return m.FeeRate
	}
	return FeeRateDefault
}

var months = map[string]int{
	"january": 1, "february": 2, "march": 3, "april": 4,
	"may": 5, "june": 6, "july": 7, "august": 8,
	"september": 9, "october": 10, "november": 11, "december": 12,
}

var et, _ = time.LoadLocation("America/New_York")

func DaysToExpiry(dateStr string) float64 {
	parts := strings.Split(dateStr, "-")
	if len(parts) < 2 {
		return 1.0
	}
	monthNum := months[strings.ToLower(parts[0])]
	if monthNum == 0 {
		return 1.0
	}
	day, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 1.0
	}
	now := time.Now().In(et)
	expiry := time.Date(now.Year(), time.Month(monthNum), day, 16, 0, 0, 0, et)
	if expiry.Before(now) {
		expiry = expiry.AddDate(1, 0, 0)
		if expiry.Before(now) {
			return 0.01
		}
	}
	days := int(expiry.Sub(now).Hours()/24) + 1
	if days < 1 {
		days = 1
	}
	return float64(days)
}
