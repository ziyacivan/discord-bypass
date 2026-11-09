package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) events(t *testing.T) []event {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []event
	sc := bufio.NewScanner(bytes.NewReader(l.b.Bytes()))
	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("geçersiz JSON olayı %q: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

// TestProxyEvents, Discord alan adının bypass, diğerinin direkt olarak
// raporlandığını ve kapanışta bayt sayılarının geldiğini doğrular.
func TestProxyEvents(t *testing.T) {
	var buf lockedBuffer
	enableEvents(&buf)
	defer setEventHandler(nil)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 1000))
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	d, err := newDialer(config{doh: "cloudflare", domains: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	local := []net.IP{net.IPv4(127, 0, 0, 1)}
	d.doh.cache["example.com"] = dnsEntry{ips: local, exp: time.Now().Add(time.Hour)}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	p := &proxy{d: d, mode: modes[0]}
	go p.serve(ln)

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	proxyURL, _ := url.Parse("http://" + ln.Addr().String())
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		Proxy:             http.ProxyURL(proxyURL),
		TLSClientConfig:   &tls.Config{RootCAs: pool, ServerName: "example.com"},
		DisableKeepAlives: true,
	}}

	// Discord listesindeki host (example.com) ve listede olmayan IP adresi.
	for _, u := range []string{"https://example.com:" + port + "/", "https://127.0.0.1:" + port + "/"} {
		resp, err := client.Get(u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	// Proxy için bağlanamayan bir hedef: fail olayı üretmeli.
	if resp, err := client.Get("https://example.com:1/"); err == nil {
		resp.Body.Close()
	}

	var evs []event
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		evs = buf.events(t)
		closes := 0
		for _, e := range evs {
			if e.Type == "close" {
				closes++
			}
		}
		if closes >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	byHost := map[string]map[string]event{}
	for _, e := range evs {
		if byHost[e.Host] == nil {
			byHost[e.Host] = map[string]event{}
		}
		byHost[e.Host][e.Type] = e
	}

	ex := byHost["example.com"]
	if o := ex["open"]; !o.Bypass || o.Mode != modes[0].name || o.IP != "127.0.0.1" {
		t.Errorf("example.com open olayı yanlış: %+v", o)
	}
	if c := ex["close"]; c.Down < 1000 || c.Up == 0 || c.Error != "" {
		t.Errorf("example.com close olayı yanlış: %+v", c)
	}
	if f := ex["fail"]; f.Error == "" || !f.Bypass {
		t.Errorf("bağlanılamayan hedef için fail olayı beklenirdi: %+v", f)
	}
	if o := byHost["127.0.0.1"]["open"]; o.Bypass || o.Mode != "direkt" {
		t.Errorf("listede olmayan host direkt olmalı: %+v", o)
	}
}
