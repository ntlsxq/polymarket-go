package polymarket

import (
	"fmt"
	"github.com/goccy/go-json"
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
	"github.com/rs/zerolog/log"
)

const (
	gammaAPI              = "https://gamma-api.polymarket.com"
	maxPages              = 6
	retryAttempts         = 3
	defaultScanDaysAhead  = 7
	directScanConcurrency = 8
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
		go func(c *http.Client, url string) {
			defer wg.Done()
			resp, err := c.Get(url)
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

// gammaFeeSchedule is the optional event-level fee config. feeRate ships
// as either a JSON string ("0.072") or number (0.072) — flexFloat handles
// both.
type gammaFeeSchedule struct {
	FeeRate flexFloat `json:"feeRate"`
}

// gammaMarket is one row inside an event's markets[]. clobTokenIds and
// outcomePrices are JSON-encoded string arrays embedded in the response
// — i.e. a string field whose contents must be re-parsed.
type gammaMarket struct {
	Slug                  string     `json:"slug"`
	ConditionID           string     `json:"conditionId"`
	GroupItemTitle        string     `json:"groupItemTitle"`
	GroupItemThreshold    flexInt    `json:"groupItemThreshold"`
	ClobTokenIDs          string     `json:"clobTokenIds"`
	OutcomePrices         string     `json:"outcomePrices"`
	OrderPriceMinTickSize flexString `json:"orderPriceMinTickSize"`
	VolumeNum             flexFloat  `json:"volumeNum"`
}

type gammaEvent struct {
	Slug            string            `json:"slug"`
	NegRiskMarketID string            `json:"negRiskMarketID"`
	Volume24hr      flexFloat         `json:"volume24hr"`
	FeeSchedule     *gammaFeeSchedule `json:"feeSchedule,omitempty"`
	Markets         []gammaMarket     `json:"markets"`
}

// ScanMarkets queries Polymarket's Gamma API and returns markets matching
// the given coins and dates. Pass nil for dates to scan today's ET date
// through the next week, matching Polymarket's rolling daily crypto markets.
// Pass an empty coins slice to include none; typical usage is to pass AllCoins
// or a subset.
func ScanMarkets(coins, dates []string) ([]Market, error) {
	coinSet := make(map[string]struct{}, len(coins))
	for _, c := range coins {
		coinSet[c] = struct{}{}
	}

	scanDates := dates
	if scanDates == nil {
		scanDates = upcomingDateStrings(time.Now().In(et), defaultScanDaysAhead)
	}

	var dateSet map[string]struct{}
	if scanDates != nil {
		dateSet = make(map[string]struct{}, len(scanDates))
		for _, d := range scanDates {
			dateSet[d] = struct{}{}
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	events, source, slugCount, err := fetchScanEvents(client, coins, scanDates)
	if err != nil {
		return nil, err
	}

	var result []Market

	for _, ev := range events {
		if ev.Slug == "" {
			continue
		}
		if !strings.Contains(ev.Slug, "-above-") && !strings.Contains(ev.Slug, "-price-") {
			continue
		}

		var coin string
		for c := range coinSet {
			if strings.HasPrefix(ev.Slug, c) {
				coin = c
				break
			}
		}
		if coin == "" {
			continue
		}

		var etype, dateStr string
		if i := strings.LastIndex(ev.Slug, "-above-on-"); i >= 0 {
			etype = "above"
			dateStr = ev.Slug[i+len("-above-on-"):]
		} else if i := strings.LastIndex(ev.Slug, "-price-on-"); i >= 0 {
			etype = "range"
			dateStr = ev.Slug[i+len("-price-on-"):]
		} else {
			continue
		}

		if strings.Contains(dateStr, "-et") {
			continue
		}

		if dateSet != nil {
			if _, ok := dateSet[dateStr]; !ok {
				continue
			}
		}

		eventFeeRate := 0.0
		if ev.FeeSchedule != nil {
			eventFeeRate = float64(ev.FeeSchedule.FeeRate)
		}

		for _, m := range ev.Markets {
			tids, err := decodeJSONStringArray(m.ClobTokenIDs)
			if err != nil || len(tids) < 2 {
				continue
			}

			prices, _ := decodeJSONStringArray(m.OutcomePrices)
			var mid float64
			if len(prices) > 0 {
				mid, _ = strconv.ParseFloat(prices[0], 64)
			}

			coinShort := CoinShort[coin]
			if coinShort == "" {
				coinShort = strings.ToUpper(coin[:3])
			}

			mk := NewMarket(coin, coinShort, dateStr, etype, m.GroupItemTitle,
				int(m.GroupItemThreshold), tids[0], tids[1], mid, m.ConditionID, string(m.OrderPriceMinTickSize))
			mk.FeeRate = eventFeeRate
			if m.Slug != "" {
				mk.Slug = m.Slug
			} else {
				mk.Slug = ev.Slug
			}

			if vn := float64(m.VolumeNum); vn > 0 {
				mk.Volume24h = vn
			} else if v24 := float64(ev.Volume24hr); v24 > 0 {
				mk.Volume24h = v24 / float64(len(ev.Markets))
			}
			if ev.NegRiskMarketID != "" && etype == "range" {
				mk.NegRiskMarketID = ev.NegRiskMarketID
				mk.QuestionIndex = DeriveQuestionIndex(ev.NegRiskMarketID, m.ConditionID)
			}
			result = append(result, mk)
		}
	}

	var dynamicFeeCount int
	feeRates := make(map[float64]int)
	for i := range result {
		rate := result[i].EffectiveFeeRate()
		feeRates[rate]++
		if result[i].FeeRate > 0 {
			dynamicFeeCount++
		}
	}
	feeLog := log.Info().Int("markets", len(result)).Int("events", len(events)).
		Int("dynamic_fee_rates", dynamicFeeCount).
		Str("source", source).
		Int("slugs", slugCount)
	for rate, count := range feeRates {
		feeLog = feeLog.Int(fmt.Sprintf("fee_%.4f", rate), count)
	}
	feeLog.Msg("[SCAN] done")

	return result, nil
}

func fetchScanEvents(client *http.Client, coins, dates []string) ([]gammaEvent, string, int, error) {
	slugs := scanEventSlugs(coins, dates)
	if len(slugs) > 0 {
		events, err := fetchEventsBySlugs(client, slugs)
		if err == nil && len(events) > 0 {
			return events, "slug", len(slugs), nil
		}
		log.Warn().
			Err(err).
			Int("slugs", len(slugs)).
			Int("events", len(events)).
			Msg("[SCAN] direct slug scan empty; falling back to broad crypto scan")
	}

	events, err := fetchEvents(client)
	return events, "broad", len(slugs), err
}

func scanEventSlugs(coins, dates []string) []string {
	if len(coins) == 0 || len(dates) == 0 {
		return nil
	}
	out := make([]string, 0, len(coins)*len(dates)*2)
	seen := make(map[string]struct{}, len(coins)*len(dates)*2)
	for _, coin := range coins {
		coin = strings.TrimSpace(coin)
		if coin == "" {
			continue
		}
		for _, date := range dates {
			date = strings.TrimSpace(date)
			if date == "" {
				continue
			}
			for _, slug := range []string{
				fmt.Sprintf("%s-above-on-%s", coin, date),
				fmt.Sprintf("%s-price-on-%s", coin, date),
			} {
				if _, ok := seen[slug]; ok {
					continue
				}
				seen[slug] = struct{}{}
				out = append(out, slug)
			}
		}
	}
	return out
}

func upcomingDateStrings(now time.Time, daysAhead int) []string {
	if daysAhead < 0 {
		daysAhead = 0
	}
	out := make([]string, 0, daysAhead+1)
	for i := 0; i <= daysAhead; i++ {
		day := now.AddDate(0, 0, i)
		out = append(out, fmt.Sprintf("%s-%d", strings.ToLower(day.Month().String()), day.Day()))
	}
	return out
}

func fetchEventsBySlugs(client *http.Client, slugs []string) ([]gammaEvent, error) {
	type job struct {
		idx  int
		slug string
	}
	type result struct {
		idx    int
		slug   string
		events []gammaEvent
		err    error
	}
	jobs := make(chan job)
	results := make(chan result, len(slugs))
	workers := directScanConcurrency
	if workers > len(slugs) {
		workers = len(slugs)
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				events, err := fetchEventsBySlug(client, j.slug)
				results <- result{idx: j.idx, slug: j.slug, events: events, err: err}
			}
		}()
	}

	for i, slug := range slugs {
		jobs <- job{idx: i, slug: slug}
	}
	close(jobs)
	wg.Wait()
	close(results)

	bySlugIndex := make([][]gammaEvent, len(slugs))
	for res := range results {
		if res.err != nil {
			return nil, fmt.Errorf("fetch event slug %s: %w", res.slug, res.err)
		}
		bySlugIndex[res.idx] = res.events
	}

	var all []gammaEvent
	seen := make(map[string]struct{}, len(slugs))
	for _, events := range bySlugIndex {
		for _, ev := range events {
			if ev.Slug != "" {
				if _, ok := seen[ev.Slug]; ok {
					continue
				}
				seen[ev.Slug] = struct{}{}
			}
			all = append(all, ev)
		}
	}
	return all, nil
}

func fetchEventsBySlug(client *http.Client, slug string) ([]gammaEvent, error) {
	endpoint := fmt.Sprintf("%s/events?slug=%s", gammaAPI, url.QueryEscape(slug))
	body, err := httpGet(client, endpoint)
	if err != nil {
		return nil, err
	}
	var batch []gammaEvent
	if err := json.Unmarshal(body, &batch); err == nil {
		return batch, nil
	}
	var single gammaEvent
	if err := json.Unmarshal(body, &single); err != nil {
		return nil, err
	}
	if single.Slug == "" {
		return nil, nil
	}
	return []gammaEvent{single}, nil
}

func fetchEvents(client *http.Client) ([]gammaEvent, error) {
	var all []gammaEvent
	for offset := 0; offset < maxPages*100; offset += 100 {
		url := fmt.Sprintf("%s/events?tag_slug=crypto&active=true&closed=false&limit=100&offset=%d",
			gammaAPI, offset)
		body, err := httpGet(client, url)
		if err != nil {
			break
		}
		var batch []gammaEvent
		if json.Unmarshal(body, &batch) != nil {
			var single gammaEvent
			if json.Unmarshal(body, &single) != nil {
				break
			}
			batch = []gammaEvent{single}
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

func httpGet(client *http.Client, url string) ([]byte, error) {
	var lastErr error
	for i := 1; i <= retryAttempts; i++ {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			backoff(i)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			backoff(i)
			continue
		}
		if resp.StatusCode != 200 {
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

// decodeJSONStringArray parses a JSON-encoded string-array field (Polymarket
// embeds clobTokenIds / outcomePrices as a stringified JSON array inside the
// outer JSON document).
func decodeJSONStringArray(raw string) ([]string, error) {
	if raw == "" {
		raw = "[]"
	}
	var out []string
	return out, json.Unmarshal([]byte(raw), &out)
}

func FetchMarketTokens(marketID string) (yesTID, noTID string, err error) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("%s/markets/%s", gammaAPI, marketID)
	body, err := httpGet(client, url)
	if err != nil {
		return "", "", fmt.Errorf("fetch market %s: %w", marketID, err)
	}
	var m gammaMarket
	if err := json.Unmarshal(body, &m); err != nil {
		return "", "", fmt.Errorf("parse market %s: %w", marketID, err)
	}
	tids, err := decodeJSONStringArray(m.ClobTokenIDs)
	if err != nil || len(tids) < 2 {
		return "", "", fmt.Errorf("market %s: missing clobTokenIds", marketID)
	}
	return tids[0], tids[1], nil
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
