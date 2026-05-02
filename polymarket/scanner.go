package polymarket

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/goccy/go-json"
	"github.com/rs/zerolog/log"
)

const (
	gammaAPI      = "https://gamma-api.polymarket.com"
	retryAttempts = 3
)

func LogRTT() {
	type ep struct {
		name string
		url  string
	}
	endpoints := []ep{
		{"gamma", gammaAPI},
		{"clob", "https://clob.polymarket.com"},
		{"relayer", "https://relayer-v2.polymarket.com"},
	}

	transport := &http.Transport{
		MaxIdleConnsPerHost: 2,
	}

	clients := make([]*http.Client, len(endpoints))
	for i := range clients {
		clients[i] = &http.Client{Timeout: 5 * time.Second, Transport: transport}
	}

	var wg sync.WaitGroup
	for i, e := range endpoints {
		wg.Add(1)
		go func(c *http.Client, endpoint string) {
			defer wg.Done()
			resp, err := c.Get(endpoint)
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}(clients[i], e.url)
	}
	wg.Wait()

	for i, e := range endpoints {
		start := time.Now()
		resp, err := clients[i].Get(e.url)
		rtt := time.Since(start)
		if err != nil {
			log.Warn().Err(err).Str("endpoint", e.name).Msg("[RTT] failed")
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		log.Info().Str("endpoint", e.name).Dur("rtt", rtt).Int("status", resp.StatusCode).Msg("[RTT]")
	}
}

type GammaFeeSchedule struct {
	FeeRate    float64 `json:"feeRate"`
	Rate       string  `json:"rate"`
	Exponent   string  `json:"exponent"`
	TakerOnly  bool    `json:"takerOnly"`
	RebateRate string  `json:"rebateRate"`
}

func (s *GammaFeeSchedule) UnmarshalJSON(raw []byte) error {
	var in struct {
		FeeRate    *flexString `json:"feeRate"`
		Rate       *flexString `json:"rate"`
		Exponent   *flexString `json:"exponent"`
		TakerOnly  bool        `json:"takerOnly"`
		RebateRate *flexString `json:"rebateRate"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	if in.Rate != nil {
		s.Rate = string(*in.Rate)
	} else if in.FeeRate != nil {
		s.Rate = string(*in.FeeRate)
	}
	if s.Rate != "" {
		if rate, err := strconv.ParseFloat(s.Rate, 64); err == nil {
			s.FeeRate = rate
		}
	}
	if in.Exponent != nil {
		s.Exponent = string(*in.Exponent)
	} else if in.FeeRate != nil {
		s.Exponent = "1"
	}
	s.TakerOnly = in.TakerOnly
	if in.RebateRate != nil {
		s.RebateRate = string(*in.RebateRate)
	}
	return nil
}

type GammaMarket struct {
	Slug                  string            `json:"slug"`
	ConditionID           string            `json:"conditionId"`
	GroupItemTitle        string            `json:"groupItemTitle"`
	GroupItemThreshold    int               `json:"groupItemThreshold"`
	ClobTokenIDs          string            `json:"clobTokenIds"`
	OutcomePrices         string            `json:"outcomePrices"`
	OrderPriceMinTickSize string            `json:"orderPriceMinTickSize"`
	VolumeNum             float64           `json:"volumeNum"`
	FeeSchedule           *GammaFeeSchedule `json:"feeSchedule,omitempty"`

	Active          *bool `json:"active,omitempty"`
	Closed          *bool `json:"closed,omitempty"`
	AcceptingOrders *bool `json:"acceptingOrders,omitempty"`
	EnableOrderBook *bool `json:"enableOrderBook,omitempty"`
}

func (m *GammaMarket) UnmarshalJSON(raw []byte) error {
	var in struct {
		Slug                  string            `json:"slug"`
		ConditionID           string            `json:"conditionId"`
		GroupItemTitle        string            `json:"groupItemTitle"`
		GroupItemThreshold    flexInt           `json:"groupItemThreshold"`
		ClobTokenIDs          string            `json:"clobTokenIds"`
		OutcomePrices         string            `json:"outcomePrices"`
		OrderPriceMinTickSize flexString        `json:"orderPriceMinTickSize"`
		VolumeNum             flexFloat         `json:"volumeNum"`
		FeeSchedule           *GammaFeeSchedule `json:"feeSchedule,omitempty"`

		Active          *bool `json:"active"`
		Closed          *bool `json:"closed"`
		AcceptingOrders *bool `json:"acceptingOrders"`
		EnableOrderBook *bool `json:"enableOrderBook"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}

	m.Slug = in.Slug
	m.ConditionID = in.ConditionID
	m.GroupItemTitle = in.GroupItemTitle
	m.GroupItemThreshold = int(in.GroupItemThreshold)
	m.ClobTokenIDs = in.ClobTokenIDs
	m.OutcomePrices = in.OutcomePrices
	m.OrderPriceMinTickSize = string(in.OrderPriceMinTickSize)
	m.VolumeNum = float64(in.VolumeNum)
	m.FeeSchedule = in.FeeSchedule
	m.Active = in.Active
	m.Closed = in.Closed
	m.AcceptingOrders = in.AcceptingOrders
	m.EnableOrderBook = in.EnableOrderBook
	return nil
}

func (m GammaMarket) ClobTokenIDList() ([]string, error) {
	return DecodeJSONStringArray(m.ClobTokenIDs)
}

func (m GammaMarket) OutcomePriceList() ([]string, error) {
	return DecodeJSONStringArray(m.OutcomePrices)
}

type GammaEvent struct {
	Slug            string            `json:"slug"`
	NegRiskMarketID string            `json:"negRiskMarketID"`
	Volume24hr      float64           `json:"volume24hr"`
	FeeSchedule     *GammaFeeSchedule `json:"feeSchedule,omitempty"`
	Markets         []GammaMarket     `json:"markets"`
}

func (e *GammaEvent) UnmarshalJSON(raw []byte) error {
	var in struct {
		Slug            string            `json:"slug"`
		NegRiskMarketID string            `json:"negRiskMarketID"`
		Volume24hr      flexFloat         `json:"volume24hr"`
		FeeSchedule     *GammaFeeSchedule `json:"feeSchedule,omitempty"`
		Markets         []GammaMarket     `json:"markets"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}

	e.Slug = in.Slug
	e.NegRiskMarketID = in.NegRiskMarketID
	e.Volume24hr = float64(in.Volume24hr)
	e.FeeSchedule = in.FeeSchedule
	e.Markets = in.Markets
	return nil
}

func FetchEventsBySlug(ctx context.Context, slug string) ([]GammaEvent, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	endpoint := fmt.Sprintf("%s/events?slug=%s", gammaAPI, url.QueryEscape(slug))
	body, err := httpGet(ctx, client, endpoint)
	if err != nil {
		return nil, err
	}

	var batch []GammaEvent
	if err := json.Unmarshal(body, &batch); err == nil {
		return batch, nil
	}

	var single GammaEvent
	if err := json.Unmarshal(body, &single); err != nil {
		return nil, err
	}
	if single.Slug == "" {
		return nil, nil
	}
	return []GammaEvent{single}, nil
}

func FetchMarket(ctx context.Context, marketID string) (*GammaMarket, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	endpoint := fmt.Sprintf("%s/markets/%s", gammaAPI, url.PathEscape(marketID))
	body, err := httpGet(ctx, client, endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetch market %s: %w", marketID, err)
	}
	var m GammaMarket
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("parse market %s: %w", marketID, err)
	}
	return &m, nil
}

func FetchMarketTokens(marketID string) (yesTID, noTID string, err error) {
	return FetchMarketTokensContext(context.Background(), marketID)
}

func FetchMarketTokensContext(ctx context.Context, marketID string) (yesTID, noTID string, err error) {
	m, err := FetchMarket(ctx, marketID)
	if err != nil {
		return "", "", err
	}
	tids, err := m.ClobTokenIDList()
	if err != nil || len(tids) < 2 {
		return "", "", fmt.Errorf("market %s: missing clobTokenIds", marketID)
	}
	return tids[0], tids[1], nil
}

func httpGet(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	var lastErr error
	for i := 1; i <= retryAttempts; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			backoff(i)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			backoff(i)
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			backoff(i)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return body, nil
	}
	return nil, fmt.Errorf("retries exhausted: %w", lastErr)
}

func backoff(attempt int) {
	if attempt < retryAttempts {
		time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
	}
}

// DecodeJSONStringArray parses Gamma fields like clobTokenIds/outcomePrices:
// JSON arrays encoded as strings inside the outer JSON object.
func DecodeJSONStringArray(raw string) ([]string, error) {
	if raw == "" {
		raw = "[]"
	}
	var out []string
	return out, json.Unmarshal([]byte(raw), &out)
}

func DeriveQuestionIndex(marketID, targetConditionID string) int {
	mID := new(big.Int)
	mID.SetString(strings.TrimPrefix(marketID, "0x"), 16)

	oracle := common.FromHex("d91E80cF2E7be2e162c6513ceD06f1dD0dA35296")
	targetCond := strings.TrimPrefix(strings.ToLower(targetConditionID), "0x")

	var outcomeSlots [32]byte
	big.NewInt(2).FillBytes(outcomeSlots[:])

	for i := 0; i < 256; i++ {
		qID := new(big.Int).Add(mID, big.NewInt(int64(i)))
		var qBytes [32]byte
		qID.FillBytes(qBytes[:])

		data := make([]byte, 0, 84)
		data = append(data, oracle...)
		data = append(data, qBytes[:]...)
		data = append(data, outcomeSlots[:]...)

		condID := fmt.Sprintf("%x", crypto.Keccak256(data))
		if condID == targetCond {
			return i
		}
	}
	return -1
}
