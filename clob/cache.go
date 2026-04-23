package clob

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/goccy/go-json"
)

// GetFeeRate fetches the CLOB base_fee (signed-order FeeRateBps, distinct
// from the strategy fee coefficient on Market.FeeRate). Called at bootstrap
// to populate Market.FeeRateBps; not on hot paths.
func (c *Client) GetFeeRate(ctx context.Context, tokenID string) (int64, error) {
	path := EndpointGetFeeRate + "?token_id=" + tokenID
	raw, err := c.doGet(ctx, path)
	if err != nil {
		return 0, err
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, err
	}

	switch v := result["base_fee"].(type) {
	case float64:
		return int64(v), nil
	case string:
		rate, _ := strconv.ParseInt(v, 10, 64)
		return rate, nil
	}
	return 0, nil
}

func (c *Client) GetTickSize(ctx context.Context, tokenID string) (string, error) {
	c.tickSizeMu.RLock()
	if ts, ok := c.tickSizeCache[tokenID]; ok {
		if time.Since(c.tickSizeCacheTime[tokenID]) < c.tickSizeTTL {
			c.tickSizeMu.RUnlock()
			return ts, nil
		}
	}
	c.tickSizeMu.RUnlock()

	path := EndpointGetTickSize + "?token_id=" + tokenID
	raw, err := c.doGet(ctx, path)
	if err != nil {
		return "", err
	}

	var tickSize string
	if err := json.Unmarshal(raw, &tickSize); err != nil {
		var obj map[string]any
		if err2 := json.Unmarshal(raw, &obj); err2 != nil {
			return "", fmt.Errorf("parse tick_size response: %w", err)
		}
		val, exists := obj["minimum_tick_size"]
		if !exists {
			val, exists = obj["tick_size"]
		}
		if !exists {
			return "", fmt.Errorf("unexpected tick_size response: %s", string(raw))
		}
		switch v := val.(type) {
		case string:
			tickSize = v
		case float64:
			tickSize = strconv.FormatFloat(v, 'f', -1, 64)
		case json.Number:
			tickSize = v.String()
		default:
			return "", fmt.Errorf("unexpected tick_size type %T in response: %s", val, string(raw))
		}
	}

	c.tickSizeMu.Lock()
	c.tickSizeCache[tokenID] = tickSize
	c.tickSizeCacheTime[tokenID] = time.Now()
	c.tickSizeMu.Unlock()
	return tickSize, nil
}
