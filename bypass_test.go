package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// captureConn, yazılan ilk veriyi (ClientHello) yakalar ve el sıkışmayı keser.
type captureConn struct {
	net.Conn
	buf bytes.Buffer
}

func (c *captureConn) Write(b []byte) (int, error) {
	c.buf.Write(b)
	return 0, errors.New("yakalandı")
}
func (c *captureConn) Read([]byte) (int, error) { return 0, io.EOF }
func (c *captureConn) Close() error             { return nil }

// realClientHello, Go'nun crypto/tls istemcisinin gerçek ClientHello'sunu üretir.
func realClientHello(t *testing.T, host string) []byte {
	t.Helper()
	c := &captureConn{}
	tls.Client(c, &tls.Config{ServerName: host}).Handshake()
	if c.buf.Len() == 0 {
		t.Fatal("ClientHello yakalanamadı")
	}
	return c.buf.Bytes()
}

func TestFindSNI(t *testing.T) {
	for _, host := range []string{"discord.com", "gateway.discord.gg", "cdn.discordapp.com"} {
		hello := realClientHello(t, host)
		off, n, ok := findSNI(hello)
		if !ok {
			t.Fatalf("%s: SNI bulunamadı", host)
		}
		if got := string(hello[off : off+n]); got != host {
			t.Fatalf("SNI = %q, beklenen %q", got, host)
		}
	}
}

func TestFindSNIRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, {0x16}, {0x17, 3, 1, 0, 5, 1, 2, 3, 4, 5}, []byte("GET / HTTP/1.1\r\n\r\n")} {
		if _, _, ok := findSNI(b); ok {
			t.Fatalf("%x için SNI bulunmamalıydı", b)
		}
	}
	// Kesik bir ClientHello panik yaratmamalı.
	hello := realClientHello(t, "discord.com")
	for i := range hello {
		findSNI(hello[:i])
	}
}

// joinRecords, TLS kayıtlarının içeriğini birleştirir; sunucunun yaptığını taklit eder.
func joinRecords(t *testing.T, b []byte) []byte {
	t.Helper()
	var out []byte
	for len(b) > 0 {
		if len(b) < 5 || b[0] != 0x16 {
			t.Fatalf("geçersiz kayıt başlığı: %x", b[:min(len(b), 5)])
		}
		n := int(binary.BigEndian.Uint16(b[3:5]))
		out = append(out, b[5:5+n]...)
		b = b[5+n:]
	}
	return out
}

func TestSplitHello(t *testing.T) {
	const host = "gateway.discord.gg"
	hello := realClientHello(t, host)

	for _, m := range modes {
		parts := splitHello(hello, m)
		joined := bytes.Join(parts, nil)

		wantParts := 1
		if m.split {
			wantParts = 2
		}
		if len(parts) != wantParts {
			t.Fatalf("%s: %d parça, beklenen %d", m.name, len(parts), wantParts)
		}

		if m.tlsrec {
			if got := joinRecords(t, joined); !bytes.Equal(got, hello[5:]) {
				t.Fatalf("%s: kayıtlar birleşince orijinal ClientHello çıkmıyor", m.name)
			}
			if countRecords(joined) != 2 {
				t.Fatalf("%s: 2 TLS kaydı bekleniyordu", m.name)
			}
		} else if !bytes.Equal(joined, hello) {
			t.Fatalf("%s: parçalar birleşince orijinal veri çıkmıyor", m.name)
		}

		if m.active() {
			for i, p := range parts {
				if m.split && bytes.Contains(p, []byte(host)) {
					t.Fatalf("%s: %d. parça host adının tamamını içeriyor", m.name, i)
				}
			}
		}
	}
}

func countRecords(b []byte) int {
	n := 0
	for len(b) >= 5 {
		b = b[5+int(binary.BigEndian.Uint16(b[3:5])):]
		n++
	}
	return n
}

func TestSplitHelloPassesNonTLS(t *testing.T) {
	data := []byte("GET / HTTP/1.1\r\nHost: discord.com\r\n\r\n")
	m, _ := modeByName("tlsrec+split")
	if parts := splitHello(data, m); len(parts) != 1 || !bytes.Equal(parts[0], data) {
		t.Fatal("TLS olmayan veri değiştirilmemeli")
	}
}

func TestMatcher(t *testing.T) {
	m := newMatcher("Example.org, ", false)
	cases := map[string]bool{
		"discord.com":          true,
		"DISCORD.COM.":         true,
		"gateway.discord.gg":   true,
		"cdn.discordapp.com":   true,
		"x.y.discord.media":    true,
		"sub.example.org":      true,
		"notdiscord.com":       false,
		"discord.com.evil.net": false,
		"google.com":           false,
	}
	for host, want := range cases {
		if got := m.match(host); got != want {
			t.Errorf("match(%q) = %v, beklenen %v", host, got, want)
		}
	}
	if !newMatcher("", true).match("anything.net") {
		t.Error("-all her şeyi eşlemeli")
	}
}

func TestParseDoH(t *testing.T) {
	urls, err := parseDoH("Cloudflare, https://dns.example/dns-query")
	if err != nil || len(urls) != 3 || urls[2] != "https://dns.example/dns-query" {
		t.Fatalf("parseDoH = %v, %v", urls, err)
	}
	for _, bad := range []string{"", "http://1.1.1.1/dns-query", "8.8.8.8", "nope"} {
		if _, err := parseDoH(bad); err == nil {
			t.Errorf("%q reddedilmeliydi", bad)
		}
	}
}

func TestParseAnswer(t *testing.T) {
	name := dnsmessage.MustNewName("discord.com.")
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{Response: true, RecursionAvailable: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	b.StartAnswers()
	cname := dnsmessage.MustNewName("x.discord.com.")
	b.CNAMEResource(dnsmessage.ResourceHeader{Name: name, Class: dnsmessage.ClassINET, TTL: 30}, dnsmessage.CNAMEResource{CNAME: cname})
	b.AResource(dnsmessage.ResourceHeader{Name: cname, Class: dnsmessage.ClassINET, TTL: 300}, dnsmessage.AResource{A: [4]byte{162, 159, 128, 233}})
	b.AResource(dnsmessage.ResourceHeader{Name: cname, Class: dnsmessage.ClassINET, TTL: 120}, dnsmessage.AResource{A: [4]byte{162, 159, 135, 232}})
	msg, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	ips, ttl, err := parseAnswer(msg)
	if err != nil || len(ips) != 2 || ips[0].String() != "162.159.128.233" || ttl != 120 {
		t.Fatalf("parseAnswer = %v, %d, %v", ips, ttl, err)
	}
}

// TestProxyEndToEnd, gerçek bir TLS sunucusuna her yöntemle proxy üzerinden
// istek atar. Sunucu (Go crypto/tls) parçalanmış ClientHello'yu kabul etmeli.
func TestProxyEndToEnd(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "merhaba "+r.Host)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	// Sertifika example.com için; DoH yerine önbelleği 127.0.0.1'e yönlendir.
	d, err := newDialer(config{doh: "cloudflare", domains: "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	d.doh.cache["example.com"] = dnsEntry{ips: []net.IP{net.IPv4(127, 0, 0, 1)}, exp: time.Now().Add(time.Hour)}

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			p := &proxy{d: d, mode: m}
			go p.serve(ln)

			proxyURL, _ := url.Parse("http://" + ln.Addr().String())
			client := &http.Client{
				Timeout: 10 * time.Second,
				Transport: &http.Transport{
					Proxy:           http.ProxyURL(proxyURL),
					TLSClientConfig: &tls.Config{RootCAs: pool},
				},
			}
			resp, err := client.Get("https://example.com:" + port + "/")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if !strings.HasPrefix(string(body), "merhaba example.com") {
				t.Fatalf("yanıt = %q", body)
			}
		})
	}
}

func TestProxyServesPAC(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	d, _ := newDialer(config{doh: "cloudflare"})
	p := &proxy{d: d, mode: modes[0], pac: pacScript(ln.Addr().String(), d.domains, false)}
	go p.serve(ln)

	resp, err := http.Get("http://" + ln.Addr().String() + "/proxy.pac")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"discord.com"`) || !strings.Contains(string(body), "PROXY "+ln.Addr().String()+"; DIRECT") {
		t.Fatalf("PAC beklenen içerikte değil:\n%s", body)
	}
}

func TestLatestAppExe(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := latestAppExe(dir, "Discord.exe"); got != "" {
		t.Fatalf("boş klasör: %q", got)
	}
	mk("app-1.0.9010/Discord.exe")
	mk("app-1.0.10000/Discord.exe") // metin sırasıyla küçük, sürüm olarak büyük
	mk("app-1.0.20000/README")      // exe'si olmayan yarım klasör atlanmalı
	mk("Update.exe")
	want := filepath.Join(dir, "app-1.0.10000", "Discord.exe")
	if got := latestAppExe(dir, "Discord.exe"); got != want {
		t.Fatalf("latestAppExe = %q, beklenen %q", got, want)
	}
}
