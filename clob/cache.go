package clob

import (
	"context"
	"fmt"
	"time"

	"github.com/goccy/go-json"
)

type feeRateResponse struct {
	BaseFee flexInt64 `json:"base_fee"`
}

type tickSizeResponse struct {
	MinimumTickSize flexString `json:"minimum_tick_size"`
	TickSize        flexString `json:"tick_size"`
}

// GetFeeRate fetches the CLOB base_fee for informational purposes. In V2,
// fees are determined at match time by the protocol, not embedded in orders.
// This endpoint can be used to display expected fees to users or for analytics.
func (c *Client) GetFeeRate(ctx context.Context, tokenID string) (int64, error) {
	path := EndpointGetFeeRate + "?token_id=" + tokenID
	raw, err := c.doGet(ctx, path)
	if err != nil {
		return 0, err
	}

	var result feeRateResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return 0, err
	}
	return int64(result.BaseFee), nil
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
		var obj tickSizeResponse
		if err2 := json.Unmarshal(raw, &obj); err2 != nil {
			return "", fmt.Errorf("parse tick_size response: %w", err)
		}
		tickSize = string(obj.MinimumTickSize)
		if tickSize == "" {
			tickSize = string(obj.TickSize)
		}
		if tickSize == "" {
			return "", fmt.Errorf("unexpected tick_size response: %s", string(raw))
		}
	}

	c.tickSizeMu.Lock()
	c.tickSizeCache[tokenID] = tickSize
	c.tickSizeCacheTime[tokenID] = time.Now()
	c.tickSizeMu.Unlock()
	return tickSize, nil
}
