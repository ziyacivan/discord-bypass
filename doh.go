package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// IP adresiyle verilen sunucular, çözümlemek için başka bir DNS'e ihtiyaç duymaz.
var dohPresets = map[string][]string{
	"cloudflare": {"https://1.1.1.1/dns-query", "https://1.0.0.1/dns-query"},
	"google":     {"https://8.8.8.8/dns-query", "https://8.8.4.4/dns-query"},
	"quad9":      {"https://9.9.9.9/dns-query", "https://149.112.112.112/dns-query"},
}

func parseDoH(s string) ([]string, error) {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if urls, ok := dohPresets[strings.ToLower(p)]; ok {
			out = append(out, urls...)
			continue
		}
		u, err := url.Parse(p)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return nil, fmt.Errorf("geçersiz DoH sunucusu %q: https:// adresi ya da cloudflare, google, quad9 olmalı", p)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errors.New("en az bir DoH sunucusu gerekli")
	}
	return out, nil
}

type dnsEntry struct {
	ips []net.IP
	exp time.Time
}

// resolver, DNS-over-HTTPS ile çözümleme yapar. Sistem DNS ayarlarına
// dokunmaz; yalnızca bu programın kendi bağlantıları için kullanılır.
type resolver struct {
	urls   []string
	client *http.Client

	mu    sync.Mutex
	cache map[string]dnsEntry
}

func newResolver(urls []string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) *resolver {
	tr := &http.Transport{
		Proxy:               nil, // Ortamdaki HTTPS_PROXY değişkeni kendi proxy'mize döngü yaratmasın.
		DialContext:         dial,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 5 * time.Second,
		IdleConnTimeout:     2 * time.Minute,
		MaxIdleConnsPerHost: 2,
	}
	return &resolver{
		urls:   urls,
		client: &http.Client{Transport: tr, Timeout: 6 * time.Second},
		cache:  make(map[string]dnsEntry),
	}
}

// lookup, host için IP adreslerini döndürür. Önce IPv4 (A) kayıtları
// sorulur; yoksa IPv6 (AAAA). Hata durumunda süresi geçmiş önbellek kaydı
// varsa o kullanılır.
func (r *resolver) lookup(ctx context.Context, host string) ([]net.IP, error) {
	host = normalizeHost(host)
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}

	r.mu.Lock()
	e, cached := r.cache[host]
	r.mu.Unlock()
	if cached && time.Now().Before(e.exp) {
		return e.ips, nil
	}

	ips, ttl, err := r.query(ctx, host, dnsmessage.TypeA)
	if err == nil && len(ips) == 0 {
		ips, ttl, err = r.query(ctx, host, dnsmessage.TypeAAAA)
	}
	if err == nil && len(ips) == 0 {
		err = fmt.Errorf("%s için kayıt yok", host)
	}
	if err != nil {
		if cached {
			return e.ips, nil
		}
		return nil, err
	}

	ttl = min(max(ttl, 60), 600)
	r.mu.Lock()
	r.cache[host] = dnsEntry{ips: ips, exp: time.Now().Add(time.Duration(ttl) * time.Second)}
	r.mu.Unlock()
	return ips, nil
}

func (r *resolver) query(ctx context.Context, host string, t dnsmessage.Type) ([]net.IP, uint32, error) {
	q, err := buildQuery(host, t)
	if err != nil {
		return nil, 0, err
	}
	var lastErr error
	for _, u := range r.urls {
		ips, ttl, err := r.exchange(ctx, u, q)
		if err == nil {
			return ips, ttl, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, 0, fmt.Errorf("DoH: %w", lastErr)
}

func (r *resolver) exchange(ctx context.Context, u string, q []byte) ([]net.IP, uint32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(q))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, 0, err
	}
	return parseAnswer(body)
}

func buildQuery(host string, t dnsmessage.Type) ([]byte, error) {
	name, err := dnsmessage.NewName(host + ".")
	if err != nil {
		return nil, err
	}
	msg := dnsmessage.Message{
		Header: dnsmessage.Header{RecursionDesired: true},
		Questions: []dnsmessage.Question{
			{Name: name, Type: t, Class: dnsmessage.ClassINET},
		},
	}
	return msg.Pack()
}

func parseAnswer(b []byte) ([]net.IP, uint32, error) {
	var p dnsmessage.Parser
	h, err := p.Start(b)
	if err != nil {
		return nil, 0, err
	}
	if h.RCode != dnsmessage.RCodeSuccess {
		return nil, 0, fmt.Errorf("DNS yanıt kodu: %v", h.RCode)
	}
	if err := p.SkipAllQuestions(); err != nil {
		return nil, 0, err
	}

	var ips []net.IP
	var ttl uint32
	for {
		ah, err := p.AnswerHeader()
		if errors.Is(err, dnsmessage.ErrSectionDone) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		switch ah.Type {
		case dnsmessage.TypeA:
			a, err := p.AResource()
			if err != nil {
				return nil, 0, err
			}
			ips = append(ips, net.IP(a.A[:]))
		case dnsmessage.TypeAAAA:
			a, err := p.AAAAResource()
			if err != nil {
				return nil, 0, err
			}
			ips = append(ips, net.IP(a.AAAA[:]))
		default:
			if err := p.SkipAnswer(); err != nil {
				return nil, 0, err
			}
			continue
		}
		if ttl == 0 || ah.TTL < ttl {
			ttl = ah.TTL
		}
	}
	return ips, ttl, nil
}
