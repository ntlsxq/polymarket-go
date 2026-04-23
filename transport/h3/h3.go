package h3

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

type Config struct {
	TLSClientConfig *tls.Config
	TokenStore      quic.TokenStore
	KeepAlive       time.Duration
	MaxIdle         time.Duration
	HandshakeIdle   time.Duration
}

type RoundTripper struct {
	tr  *quic.Transport
	rt  *http3.Transport
	udp *net.UDPConn
}

func New(cfg Config) (*RoundTripper, error) {
	if cfg.TLSClientConfig == nil {
		return nil, errors.New("h3: TLSClientConfig required")
	}
	if cfg.KeepAlive <= 0 {
		cfg.KeepAlive = 15 * time.Second
	}
	if cfg.MaxIdle <= 0 {
		cfg.MaxIdle = 60 * time.Second
	}
	if cfg.HandshakeIdle <= 0 {
		cfg.HandshakeIdle = 5 * time.Second
	}

	tlsCfg := cfg.TLSClientConfig.Clone()
	tlsCfg.NextProtos = []string{"h3"}

	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}

	tr := &quic.Transport{Conn: udp}

	qcfg := &quic.Config{
		HandshakeIdleTimeout: cfg.HandshakeIdle,
		MaxIdleTimeout:       cfg.MaxIdle,
		KeepAlivePeriod:      cfg.KeepAlive,
		TokenStore:           cfg.TokenStore,
		EnableDatagrams:      false,
	}

	rt := &http3.Transport{
		TLSClientConfig: tlsCfg,
		QUICConfig:      qcfg,
		Dial: func(ctx context.Context, addr string, tc *tls.Config, qc *quic.Config) (*quic.Conn, error) {
			udpAddr, err := net.ResolveUDPAddr("udp", addr)
			if err != nil {
				return nil, err
			}

			return tr.DialEarly(ctx, udpAddr, tc, qc)
		},
	}

	return &RoundTripper{tr: tr, rt: rt, udp: udp}, nil
}

func (r *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.rt.RoundTrip(req)
}

func (r *RoundTripper) Close() error {
	_ = r.rt.Close()
	_ = r.tr.Close()
	return r.udp.Close()
}

func (r *RoundTripper) CloseIdleConnections() { r.rt.CloseIdleConnections() }
