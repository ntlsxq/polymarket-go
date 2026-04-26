package clob

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goccy/go-json"
)

// stubClient builds a *Client wired to an httptest.Server so DELETE /orders
// and DELETE /cancel-all round-trips can be observed end-to-end. We bypass
// InitAuth (which would call out to the real CLOB to derive an API secret)
// by injecting throwaway creds directly — the stub server doesn't validate
// HMAC, so any well-formed creds work.
//
// privateKeyHex is a deterministic well-known throwaway key from go-ethereum
// docs; never used on-chain.
func stubClient(t *testing.T, host string) *Client {
	t.Helper()
	const testPK = "4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318"
	c, err := NewClient(host, testPK, 137, 1, "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.creds = &ApiCreds{ApiKey: "k", ApiSecret: "c2VjcmV0", ApiPassphrase: "p"}
	return c
}

// TestCancelOrders_PartialFailure pins the load-bearing invariant: when CLOB
// returns 200 with a mix of canceled + not_canceled IDs, CancelOrders surfaces
// BOTH halves to the caller. This is the contract that lets grid.cancelBatch
// keep its partitioner reservations in sync with the matcher's view of the
// world (see internal/quote/sync.go cancelBatch).
func TestCancelOrders_PartialFailure(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"canceled": ["0xa1", "0xa2"],
			"not_canceled": {"0xb1": "order not found", "0xb2": "owner mismatch"}
		}`))
	}))
	defer srv.Close()

	c := stubClient(t, srv.URL)
	resp, err := c.CancelOrders(context.Background(), []string{"0xa1", "0xa2", "0xb1", "0xb2"})
	if err != nil {
		t.Fatalf("CancelOrders: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != EndpointCancelOrders {
		t.Fatalf("wrong upstream call: %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, "0xa1") || !strings.Contains(gotBody, "0xb2") {
		t.Fatalf("body missing IDs: %q", gotBody)
	}
	if got := len(resp.Canceled); got != 2 {
		t.Fatalf("Canceled len = %d, want 2; resp=%+v", got, resp)
	}
	if got := len(resp.NotCanceled); got != 2 {
		t.Fatalf("NotCanceled len = %d, want 2; resp=%+v", got, resp)
	}
	if resp.NotCanceled["0xb1"] != "order not found" {
		t.Fatalf("NotCanceled[0xb1] = %q", resp.NotCanceled["0xb1"])
	}
}

func TestCancelOrders_AllSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"canceled":["0x1","0x2"],"not_canceled":{}}`))
	}))
	defer srv.Close()

	c := stubClient(t, srv.URL)
	resp, err := c.CancelOrders(context.Background(), []string{"0x1", "0x2"})
	if err != nil {
		t.Fatalf("CancelOrders: %v", err)
	}
	if len(resp.Canceled) != 2 || len(resp.NotCanceled) != 0 {
		t.Fatalf("unexpected resp: %+v", resp)
	}
}

// TestCancelOrders_HTTPError pins that a network/non-2xx error returns a
// non-nil error AND a nil response — callers (cancelBatch, hedge.sell.go)
// rely on this to short-circuit before touching local state, leaving
// LiveOrders + partitioner alone.
func TestCancelOrders_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer srv.Close()

	c := stubClient(t, srv.URL)
	resp, err := c.CancelOrders(context.Background(), []string{"0x1"})
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if resp != nil {
		t.Fatalf("expected nil resp on error, got %+v", resp)
	}
}

func TestCancelAll_ParsesResponse(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"canceled":["0xfeed","0xface"],"not_canceled":{"0xdead":"already filled"}}`))
	}))
	defer srv.Close()

	c := stubClient(t, srv.URL)
	resp, err := c.CancelAll(context.Background())
	if err != nil {
		t.Fatalf("CancelAll: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != EndpointCancelAll {
		t.Fatalf("wrong upstream: %s %s", gotMethod, gotPath)
	}
	if len(resp.Canceled) != 2 || resp.Canceled[1] != "0xface" {
		t.Fatalf("Canceled wrong: %+v", resp.Canceled)
	}
	if resp.NotCanceled["0xdead"] != "already filled" {
		t.Fatalf("NotCanceled wrong: %+v", resp.NotCanceled)
	}
}

// TestCancelAll_EmptyBody pins the defensive nil-body path: some matcher
// versions can return an empty body when no orders exist server-side.
// CancelAll must return a zero-value (but non-nil) response so callers
// don't NPE on resp.Canceled.
func TestCancelAll_EmptyBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Default 200, no body.
	}))
	defer srv.Close()

	c := stubClient(t, srv.URL)
	resp, err := c.CancelAll(context.Background())
	if err != nil {
		t.Fatalf("CancelAll: %v", err)
	}
	if resp == nil {
		t.Fatal("nil resp on empty body")
	}
	if len(resp.Canceled) != 0 || len(resp.NotCanceled) != 0 {
		t.Fatalf("expected zero-value resp, got %+v", resp)
	}
}

// TestCancelOrders_BodyShape pins the exact wire body — a bare JSON array of
// IDs, not wrapped in {"orderIds": [...]}. py-clob-client and the docs both
// show this shape; if it ever drifts, every cancel silently 4xx's.
func TestCancelOrders_BodyShape(t *testing.T) {
	var got json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"canceled":[],"not_canceled":{}}`))
	}))
	defer srv.Close()

	c := stubClient(t, srv.URL)
	if _, err := c.CancelOrders(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatalf("CancelOrders: %v", err)
	}
	var parsed []string
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("body is not a JSON array: err=%v body=%s", err, got)
	}
	if len(parsed) != 2 || parsed[0] != "a" || parsed[1] != "b" {
		t.Fatalf("body shape drift: %s", got)
	}
}
