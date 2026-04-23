package clob

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/rs/zerolog/log"
)

// PrefetchFeeRates fetches the CLOB base_fee for each tokenID in parallel
// (up to 20 inflight) and calls store(tokenID, bps) for each success. Meant
// for bootstrap — caller writes the rate onto its domain type (Market).
func (c *Client) PrefetchFeeRates(ctx context.Context, tokenIDs []string, store func(string, int64)) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)
	var ok int64
	var mu sync.Mutex
	for _, tid := range tokenIDs {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			rate, err := c.GetFeeRate(ctx, id)
			if err != nil {
				return
			}
			store(id, rate)
			mu.Lock()
			ok++
			mu.Unlock()
		}(tid)
	}
	wg.Wait()
	log.Info().Int("fetched", int(ok)).Int("requested", len(tokenIDs)).Msg("[CLOB] fee_rates_prefetched")
}

func (c *Client) PrefetchTickSizes(ctx context.Context, tokenIDs []string, store func(string, string)) error {
	var mu sync.Mutex
	var failed []string
	total := len(tokenIDs)
	done := 0

	for i := 0; i < total; i += 2 {
		batch := tokenIDs[i:min(i+2, total)]
		var wg sync.WaitGroup
		for _, tid := range batch {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				var ts string
				var err error
				for attempt := 0; attempt < 3; attempt++ {
					ts, err = c.GetTickSize(ctx, id)
					if err == nil && ts != "" {
						break
					}
				}
				if err != nil || ts == "" {
					log.Debug().Err(err).Str("token", id).Msg("[CLOB] tick_size failed")
					mu.Lock()
					failed = append(failed, id)
					mu.Unlock()
					return
				}
				store(id, ts)
			}(tid)
		}
		wg.Wait()
		done += len(batch)
		if done%100 == 0 {
			log.Info().Int("done", done).Int("total", total).Msg("[CLOB] tick_sizes progress")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("tick_sizes: %d/%d failed, first: %s", len(failed), total, failed[0])
	}
	log.Info().Int("tokens", total).Msg("[CLOB] tick_sizes_prefetched")
	return nil
}

func (c *Client) GetBalanceAllowance(ctx context.Context) (map[string]any, error) {
	path := EndpointGetBalanceAllowance + "?asset_type=COLLATERAL&signature_type=" + fmt.Sprintf("%d", c.sigType)
	raw, err := c.doGet(ctx, path)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse balance_allowance response: %w", err)
	}
	return result, nil
}

func (c *Client) GetBalance(ctx context.Context) (float64, error) {
	result, err := c.GetBalanceAllowance(ctx)
	if err != nil {
		return 0, err
	}

	switch v := result["balance"].(type) {
	case float64:
		return v, nil
	case string:
		var f float64
		_, _ = fmt.Sscanf(v, "%f", &f)
		return f, nil
	default:
		return 0, fmt.Errorf("unexpected balance type: %T", v)
	}
}

func (c *Client) GetOrders(ctx context.Context) ([]map[string]any, error) {
	var allOrders []map[string]any
	cursor := "MA=="

	for {
		path := EndpointOrders + "?next_cursor=" + cursor
		raw, err := c.doGet(ctx, path)
		if err != nil {
			return nil, err
		}

		log.Debug().RawJSON("raw", raw).Msg("[CLOB] get_orders_response")

		var page []map[string]any
		if err := json.Unmarshal(raw, &page); err != nil {
			var wrapped struct {
				Data       []map[string]any `json:"data"`
				NextCursor string           `json:"next_cursor"`
			}
			if err2 := json.Unmarshal(raw, &wrapped); err2 != nil {
				return nil, fmt.Errorf("parse orders response: %w", err)
			}
			allOrders = append(allOrders, wrapped.Data...)
			if wrapped.NextCursor == "" || wrapped.NextCursor == "LTE=" {
				break
			}
			cursor = wrapped.NextCursor
			continue
		}

		allOrders = append(allOrders, page...)
		break
	}

	return allOrders, nil
}

func (c *Client) PostHeartbeat(ctx context.Context, heartbeatID string) (map[string]any, error) {
	var body map[string]any
	if heartbeatID != "" {
		body = map[string]any{"heartbeat_id": heartbeatID}
	} else {
		body = map[string]any{"heartbeat_id": nil}
	}

	raw, err := c.doPost(ctx, EndpointPostHeartbeat, body)
	if err != nil {
		errStr := err.Error()
		if idx := strings.Index(errStr, "{"); idx >= 0 {
			jsonPart := errStr[idx:]
			if end := strings.LastIndex(jsonPart, "}"); end >= 0 {
				jsonPart = jsonPart[:end+1]
			}
			var errResp map[string]any
			if json.Unmarshal([]byte(jsonPart), &errResp) == nil {
				if hbID, ok := errResp["heartbeat_id"].(string); ok && hbID != "" {
					return errResp, nil
				}
			}
		}
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse heartbeat response: %w", err)
	}
	return result, nil
}
