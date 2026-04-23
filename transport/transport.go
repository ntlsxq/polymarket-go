package transport

import (
	"fmt"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/ntlsxq/polymarket-go/transport/h3"
	"github.com/ntlsxq/polymarket-go/transport/session"
	"github.com/ntlsxq/polymarket-go/transport/tlsconf"
)

type Config struct {
	Host string
	Port string

	SessionPath string

	DialTimeout time.Duration
}

type Transport struct {
	rt    *h3.RoundTripper
	store *session.Store
}

func New(cfg Config) (*Transport, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("transport: Host required")
	}
	if cfg.Port == "" {
		cfg.Port = "443"
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}

	store := session.NewStore(cfg.SessionPath)

	tlsCfg := tlsconf.New(tlsconf.Params{
		ServerName: cfg.Host,
		NextProtos: []string{"h3"},
		Store:      store,
	})

	rt, err := h3.New(h3.Config{
		TLSClientConfig: tlsCfg,
		TokenStore:      quic.NewLRUTokenStore(16, 4),
	})
	if err != nil {
		return nil, fmt.Errorf("h3: %w", err)
	}

	return &Transport{rt: rt, store: store}, nil
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.rt.RoundTrip(req)
}

func (t *Transport) Close() error {
	if t.rt != nil {
		_ = t.rt.Close()
	}
	if t.store != nil {
		_ = t.store.Flush()
	}
	return nil
}
