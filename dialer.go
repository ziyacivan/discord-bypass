package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"time"
)

type dialer struct {
	nd      net.Dialer
	doh     *resolver
	match   func(host string) bool
	domains []string
}

func newDialer(c config) (*dialer, error) {
	ctrl, err := bindControl(c.iface)
	if err != nil {
		return nil, fmt.Errorf("-iface %q: %w", c.iface, err)
	}
	urls, err := parseDoH(c.doh)
	if err != nil {
		return nil, err
	}
	nd := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: ctrl}
	m := newMatcher(c.domains, c.all)
	return &dialer{
		nd:      nd,
		doh:     newResolver(urls, nd.DialContext),
		match:   m.match,
		domains: m.domains,
	}, nil
}

// dial, host:port'a bağlanır. Discord alan adları DoH ile çözülür (sistem
// DNS'i zehirli olsa bile doğru IP'ye gidilir); diğerleri sistem DNS'iyle,
// o da başarısız olursa DoH ile. bypass, ClientHello'nun parçalanıp
// parçalanmayacağını belirtir.
func (d *dialer) dial(ctx context.Context, host, port string) (conn net.Conn, bypass bool, err error) {
	bypass = d.match(host)

	if net.ParseIP(host) != nil {
		conn, err = d.nd.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
		return conn, bypass, err
	}

	if bypass {
		ips, err := d.doh.lookup(ctx, host)
		if err != nil {
			log.Printf("uyarı: %s DoH ile çözülemedi (%v), sistem DNS'i deneniyor", host, err)
			conn, err = d.nd.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
			return conn, true, err
		}
		conn, err = d.dialIPs(ctx, ips, port)
		return conn, true, err
	}

	conn, err = d.nd.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	var dnsErr *net.DNSError
	if err != nil && errors.As(err, &dnsErr) {
		if ips, derr := d.doh.lookup(ctx, host); derr == nil {
			conn, err = d.dialIPs(ctx, ips, port)
		}
	}
	return conn, false, err
}

func (d *dialer) dialIPs(ctx context.Context, ips []net.IP, port string) (net.Conn, error) {
	var lastErr error
	for _, ip := range ips {
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		c, err := d.nd.DialContext(cctx, "tcp", net.JoinHostPort(ip.String(), port))
		cancel()
		if err == nil {
			return c, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("IP adresi yok")
	}
	return nil, lastErr
}
