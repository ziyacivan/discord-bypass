package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// proxy, yalnızca 127.0.0.1'de dinleyen bir HTTP proxy'sidir. Discord
// (Chromium) HTTPS ve WebSocket bağlantılarını CONNECT ile açar.
type proxy struct {
	d       *dialer
	mode    mode
	pac     string
	verbose bool
	nextID  atomic.Uint64
	conns   sync.Map // id → [2]net.Conn; durdurulunca hepsi kapatılır
}

// track, açık tüneli kaydeder; dönen fonksiyon kaydı siler.
func (p *proxy) track(id uint64, a, b net.Conn) func() {
	p.conns.Store(id, [2]net.Conn{a, b})
	return func() { p.conns.Delete(id) }
}

// closeAll, açık tüm tünelleri keser. Bypass kapatıldığında Discord'un eski
// bağlantılarla çalışmaya devam etmemesi için kullanılır.
func (p *proxy) closeAll() {
	p.conns.Range(func(k, v any) bool {
		pair := v.([2]net.Conn)
		pair[0].Close()
		pair[1].Close()
		p.conns.Delete(k)
		return true
	})
}

func (p *proxy) serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go p.handle(c)
	}
}

func (p *proxy) handle(c net.Conn) {
	defer c.Close()

	c.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	c.SetReadDeadline(time.Time{})

	switch {
	case req.Method == http.MethodConnect:
		p.handleConnect(c, br, req)
	case req.URL.IsAbs():
		p.handleHTTP(c, req)
	case req.URL.Path == "/proxy.pac":
		writeResponse(c, http.StatusOK, "application/x-ns-proxy-autoconfig", p.pac)
	case req.URL.Path == "/":
		writeResponse(c, http.StatusOK, "text/plain; charset=utf-8", "discord-bypass çalışıyor ("+p.mode.name+")\n")
	default:
		writeResponse(c, http.StatusNotFound, "text/plain; charset=utf-8", "bulunamadı\n")
	}
}

func (p *proxy) handleConnect(c net.Conn, br *bufio.Reader, req *http.Request) {
	host, port, err := net.SplitHostPort(req.Host)
	if err != nil {
		writeResponse(c, http.StatusBadRequest, "text/plain; charset=utf-8", "geçersiz CONNECT hedefi\n")
		return
	}

	id := p.nextID.Add(1)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	srv, bypass, err := p.d.dial(ctx, host, port)
	cancel()
	if err != nil {
		if bypass || p.verbose {
			log.Printf("bağlanılamadı %s:%s: %v", host, port, err)
		}
		emit(event{Type: "fail", ID: id, Host: host, Port: port, Bypass: bypass, Error: shortErr(err)})
		writeResponse(c, http.StatusBadGateway, "text/plain; charset=utf-8", "hedefe bağlanılamadı\n")
		return
	}
	defer srv.Close()
	defer p.track(id, c, srv)()

	if _, err := io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}

	m := mode{name: "direkt"}
	if bypass {
		m = p.mode
	}
	if p.verbose {
		log.Printf("%s:%s → %s [%s]", host, port, srv.RemoteAddr(), m.name)
	}
	emit(event{Type: "open", ID: id, Host: host, Port: port, IP: remoteIP(srv), Bypass: bypass, Mode: m.name})
	up, down, rerr := relay(c, br, srv, m)
	emit(event{Type: "close", ID: id, Host: host, Port: port, Bypass: bypass, Up: up, Down: down,
		Ms: time.Since(start).Milliseconds(), Error: closeErr(rerr)})
}

// handleHTTP, şifresiz http:// isteklerini (absolute-form) iletir. Discord
// neredeyse her şeyi HTTPS ile yapar; bu yalnızca eksiksizlik için var.
func (p *proxy) handleHTTP(c net.Conn, req *http.Request) {
	if req.URL.Scheme != "http" {
		writeResponse(c, http.StatusBadRequest, "text/plain; charset=utf-8", "desteklenmeyen şema\n")
		return
	}
	host, port := req.URL.Hostname(), req.URL.Port()
	if port == "" {
		port = "80"
	}

	id := p.nextID.Add(1)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	srv, bypass, err := p.d.dial(ctx, host, port)
	cancel()
	if err != nil {
		emit(event{Type: "fail", ID: id, Host: host, Port: port, Bypass: bypass, Error: shortErr(err)})
		writeResponse(c, http.StatusBadGateway, "text/plain; charset=utf-8", "hedefe bağlanılamadı\n")
		return
	}
	defer srv.Close()
	defer p.track(id, c, srv)()
	emit(event{Type: "open", ID: id, Host: host, Port: port, IP: remoteIP(srv), Bypass: bypass, Mode: "http"})

	req.Header.Del("Proxy-Connection")
	req.Header.Del("Proxy-Authorization")
	req.Close = true
	var down int64
	if err = req.Write(srv); err == nil {
		down, err = io.Copy(c, srv)
	}
	emit(event{Type: "close", ID: id, Host: host, Port: port, Bypass: bypass, Down: down,
		Ms: time.Since(start).Milliseconds(), Error: closeErr(err)})
}

// relay, iki yönlü veri aktarır ve gönderilen/alınan bayt sayısını döndürür.
// İstemciden gelen ilk TLS kaydı (ClientHello) seçilen yönteme göre
// parçalanarak gönderilir. Dönen hata yalnızca sunucu tarafındaki okuma
// hatasıdır (ör. DPI'ın gönderdiği RST); istemcinin kapatması hata sayılmaz.
func relay(client net.Conn, clientR *bufio.Reader, server net.Conn, m mode) (up, down int64, srvErr error) {
	type result struct {
		n   int64
		err error
	}
	upc, downc := make(chan result, 1), make(chan result, 1)
	sr := &trackReader{r: server}

	go func() {
		n, err := io.Copy(client, sr)
		closeWrite(client)
		downc <- result{n, err}
	}()

	go func() {
		var n int64
		var err error
		if m.active() {
			n, err = forwardHello(server, clientR, m)
		}
		if err == nil {
			var k int64
			k, err = io.Copy(server, clientR)
			n += k
		}
		closeWrite(server)
		upc <- result{n, err}
	}()

	var first, second result
	firstUp := false
	select {
	case first = <-upc:
		firstUp = true
	case first = <-downc:
	}

	// Bir yön bittiğinde diğerine kalan veriyi aktarması için süre tanı.
	grace := 30 * time.Second
	if first.err != nil {
		grace = 0
	}
	deadline := time.Now().Add(grace)
	client.SetDeadline(deadline)
	server.SetDeadline(deadline)

	if firstUp {
		second = <-downc
		up, down = first.n, second.n
	} else {
		second = <-upc
		up, down = second.n, first.n
	}

	if err := sr.err; err != nil && !errors.Is(err, os.ErrDeadlineExceeded) && !errors.Is(err, net.ErrClosed) {
		srvErr = err
	}
	return up, down, srvErr
}

// trackReader, sunucudan okurken oluşan hatayı (EOF hariç) saklar.
type trackReader struct {
	r   io.Reader
	err error
}

func (t *trackReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && t.err == nil {
		t.err = err
	}
	return n, err
}

// forwardHello, istemcinin ilk TLS kaydını okur ve parçalayarak iletir.
// Veri TLS değilse hiçbir şey tüketmeden döner; io.Copy olduğu gibi aktarır.
func forwardHello(server net.Conn, r *bufio.Reader, m mode) (int64, error) {
	b, err := r.Peek(1)
	if err != nil || b[0] != 0x16 {
		return 0, nil
	}
	// TLS istemcileri ClientHello kaydını bir bütün olarak gönderir; beklemek güvenli.
	hdr, err := r.Peek(5)
	if err != nil || hdr[1] != 0x03 {
		return 0, nil
	}
	n := int(binary.BigEndian.Uint16(hdr[3:5]))
	if n == 0 || n > 18432 {
		return 0, nil
	}
	hello := make([]byte, 5+n)
	if _, err := io.ReadFull(r, hello); err != nil {
		return 0, err
	}
	if err := writeHello(server, hello, m); err != nil {
		return 0, err
	}
	return int64(len(hello)), nil
}

func remoteIP(c net.Conn) string {
	if ta, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return ta.IP.String()
	}
	return ""
}

func closeErr(err error) string {
	if err == nil || errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, net.ErrClosed) {
		return ""
	}
	return shortErr(err)
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		cw.CloseWrite()
	}
}

func writeResponse(c net.Conn, code int, ctype, body string) {
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), ctype, len(body), body)
}

// pacScript, tarayıcılar için yalnızca Discord alan adlarını proxy'ye
// yönlendiren bir PAC dosyası üretir. Proxy kapalıysa tarayıcı DIRECT'e düşer.
func pacScript(addr string, domains []string, all bool) string {
	var sb strings.Builder
	sb.WriteString("function FindProxyForURL(url, host) {\n")
	sb.WriteString("  host = host.toLowerCase();\n")
	sb.WriteString("  if (isPlainHostName(host) || host === \"localhost\" || shExpMatch(host, \"127.*\")) return \"DIRECT\";\n")
	if all {
		fmt.Fprintf(&sb, "  return \"PROXY %s; DIRECT\";\n}\n", addr)
		return sb.String()
	}
	quoted := make([]string, len(domains))
	for i, d := range domains {
		quoted[i] = `"` + d + `"`
	}
	fmt.Fprintf(&sb, "  var d = [%s];\n", strings.Join(quoted, ", "))
	sb.WriteString("  for (var i = 0; i < d.length; i++) {\n")
	fmt.Fprintf(&sb, "    if (host === d[i] || dnsDomainIs(host, \".\" + d[i])) return \"PROXY %s; DIRECT\";\n", addr)
	sb.WriteString("  }\n  return \"DIRECT\";\n}\n")
	return sb.String()
}
