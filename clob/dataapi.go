package clob

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/goccy/go-json"
)

const dataAPIBase = "https://data-api.polymarket.com"

var dataAPIClient = &http.Client{Timeout: 30 * time.Second}

func (c *Client) GetInventory(ctx context.Context) ([]Position, error) {
	const pageSize = 500
	var all []Position

	for offset := 0; ; offset += pageSize {
		url := fmt.Sprintf("%s/positions?user=%s&sizeThreshold=0&limit=%d&offset=%d",
			dataAPIBase, strings.ToLower(c.funder.Hex()), pageSize, offset)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("get inventory: %w", err)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := dataAPIClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("get inventory: %w", err)
		}

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("data-api /positions returned %d: %s", resp.StatusCode, string(body))
		}

		var page []Position
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("parse positions: %w", err)
		}

		all = append(all, page...)
		if len(page) < pageSize {
			break
		}
	}
	return all, nil
}

func (c *Client) GetTrades(ctx context.Context, afterUnix int64) ([]Trade, error) {
	path := fmt.Sprintf("%s?maker_address=%s&after=%d",
		EndpointGetTrades, strings.ToLower(c.funder.Hex()), afterUnix)
	raw, err := c.doGet(ctx, path)
	if err != nil {
		return nil, err
	}

	var wrapped struct {
		Data []Trade `json:"data"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Data != nil {
		return wrapped.Data, nil
	}
	var trades []Trade
	if err := json.Unmarshal(raw, &trades); err != nil {
		return nil, fmt.Errorf("parse trades response: %w", err)
	}
	return trades, nil
}
