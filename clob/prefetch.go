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

func (c *Client) GetBalanceAllowance(ctx context.Context) (*BalanceAllowance, error) {
	path := EndpointGetBalanceAllowance + "?asset_type=COLLATERAL&signature_type=" + fmt.Sprintf("%d", c.sigType)
	raw, err := c.doGet(ctx, path)
	if err != nil {
		return nil, err
	}

	var result BalanceAllowance
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse balance_allowance response: %w", err)
	}
	return &result, nil
}

func (c *Client) GetBalance(ctx context.Context) (float64, error) {
	ba, err := c.GetBalanceAllowance(ctx)
	if err != nil {
		return 0, err
	}
	if ba.Balance == "" {
		return 0, nil
	}
	var f float64
	if _, err := fmt.Sscanf(ba.Balance, "%f", &f); err != nil {
		return 0, fmt.Errorf("parse balance %q: %w", ba.Balance, err)
	}
	return f, nil
}

// GetOrders fetches all open orders for the authenticated user, paging through
// next_cursor until LTE= (sentinel for last page).
func (c *Client) GetOrders(ctx context.Context) ([]Order, error) {
	var all []Order
	cursor := "MA=="

	for {
		path := EndpointOrders + "?next_cursor=" + cursor
		raw, err := c.doGet(ctx, path)
		if err != nil {
			return nil, err
		}
		log.Debug().RawJSON("raw", raw).Msg("[CLOB] get_orders_response")

		// Two wire shapes exist: bare array (legacy) and {data, next_cursor, ...}.
		var page []Order
		if err := json.Unmarshal(raw, &page); err == nil {
			all = append(all, page...)
			break
		}
		var wrapped struct {
			Data       []Order `json:"data"`
			NextCursor string  `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil {
			return nil, fmt.Errorf("parse orders response: %w", err)
		}
		all = append(all, wrapped.Data...)
		if wrapped.NextCursor == "" || wrapped.NextCursor == "LTE=" {
			break
		}
		cursor = wrapped.NextCursor
	}

	return all, nil
}

// GetOrder fetches a single open order by its ID via GET /data/order/{id}.
func (c *Client) GetOrder(ctx context.Context, orderID string) (*Order, error) {
	if orderID == "" {
		return nil, fmt.Errorf("GetOrder: orderID is empty")
	}
	raw, err := c.doGet(ctx, EndpointGetOrder+orderID)
	if err != nil {
		return nil, err
	}
	var o Order
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, fmt.Errorf("parse order response: %w", err)
	}
	return &o, nil
}

type HeartbeatResponse struct {
	HeartbeatID string `json:"heartbeat_id"`
}

func (c *Client) PostHeartbeat(ctx context.Context, heartbeatID string) (*HeartbeatResponse, error) {
	body := map[string]any{"heartbeat_id": nil}
	if heartbeatID != "" {
		body["heartbeat_id"] = heartbeatID
	}

	raw, err := c.doPost(ctx, EndpointPostHeartbeat, body)
	if err != nil {
		// Server returns the allocated heartbeat_id inside a 4xx body on the
		// very first call — recover it from the error string.
		errStr := err.Error()
		if idx := strings.Index(errStr, "{"); idx >= 0 {
			jsonPart := errStr[idx:]
			if end := strings.LastIndex(jsonPart, "}"); end >= 0 {
				jsonPart = jsonPart[:end+1]
			}
			var hb HeartbeatResponse
			if json.Unmarshal([]byte(jsonPart), &hb) == nil && hb.HeartbeatID != "" {
				return &hb, nil
			}
		}
		return nil, err
	}

	var hb HeartbeatResponse
	if err := json.Unmarshal(raw, &hb); err != nil {
		return nil, fmt.Errorf("parse heartbeat response: %w", err)
	}
	return &hb, nil
}
