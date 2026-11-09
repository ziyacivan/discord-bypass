package main

import (
	"fmt"
	"net"
)

// engine, dinlenen port ile çalışan proxy'yi bir arada tutar. Terminal
// sürümü bir kez başlatır; tepsi uygulaması aç/kapa ile defalarca başlatıp
// durdurur.
type engine struct {
	ln   net.Listener
	p    *proxy
	mode mode
	addr string
}

// startEngine, 127.0.0.1:port'u dinler, çalışan yöntemi seçer ve proxy'yi başlatır.
func startEngine(c config, d *dialer) (*engine, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", c.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%s dinlenemedi; program zaten açık olabilir ya da port kullanımda: %w", addr, err)
	}
	m, err := pickMode(c.mode, d)
	if err != nil {
		ln.Close()
		return nil, err
	}
	p := &proxy{d: d, mode: m, verbose: c.verbose, pac: pacScript(addr, d.domains, c.all)}
	go p.serve(ln)
	return &engine{ln: ln, p: p, mode: m, addr: addr}, nil
}

func (e *engine) proxyURL() string { return "http://" + e.addr }

// stop, yeni bağlantıları reddeder ve açık tünelleri keser.
func (e *engine) stop() {
	e.ln.Close()
	e.p.closeAll()
}
