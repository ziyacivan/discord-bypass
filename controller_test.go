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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubDiscord, testler sırasında gerçek Discord'un kapatılıp açılmasını engeller.
func stubDiscord(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	old := discordLauncher
	discordLauncher = func(_, proxyURL string, force bool) error {
		calls = append(calls, fmt.Sprintf("%s force=%v", proxyURL, force))
		return nil
	}
	t.Cleanup(func() { discordLauncher = old })
	return &calls
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func newTestController(t *testing.T) (*controller, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	cfg := config{port: freePort(t), doh: "cloudflare"}
	c := newController(cfg, dir, "127.0.0.1:8897")
	srv := httptest.NewServer(c.handler())
	t.Cleanup(srv.Close)
	t.Cleanup(c.shutdown)
	return c, srv
}

func call(t *testing.T, srv *httptest.Server, c *controller, method, path, body string, hdr map[string]string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	req.Host = "127.0.0.1:8897"
	if c != nil {
		req.Header.Set("X-Token", c.token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestPanelSecurity(t *testing.T) {
	c, srv := newTestController(t)

	// Başka bir Host başlığı (DNS rebinding) reddedilmeli.
	req, _ := http.NewRequest("GET", srv.URL+"/api/state", nil)
	req.Host = "evil.example:8897"
	req.Header.Set("X-Token", c.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("yabancı Host: %d, beklenen 403", resp.StatusCode)
	}

	// Token olmadan API kapalı, panel sayfası ve PAC açık.
	if code, _ := call(t, srv, nil, "GET", "/api/state", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("token'sız state: %d", code)
	}
	if code, _ := call(t, srv, nil, "POST", "/api/action", `{"action":"start"}`, nil); code != http.StatusUnauthorized {
		t.Fatalf("token'sız action: %d", code)
	}
	if code, b := call(t, srv, nil, "GET", "/", "", nil); code != 200 || !bytes.Contains(b, []byte("Discord Bypass")) {
		t.Fatalf("panel sayfası: %d", code)
	}
	if code, b := call(t, srv, nil, "GET", "/proxy.pac", "", nil); code != 200 || !bytes.Contains(b, []byte(`"discord.com"`)) {
		t.Fatalf("PAC: %d %s", code, b)
	}

	// Form gönderimi (CSRF) JSON olmadığı için reddedilmeli.
	if code, _ := call(t, srv, c, "POST", "/api/action", "", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); code != http.StatusMethodNotAllowed {
		t.Fatalf("form içerik türü: %d", code)
	}
}

func TestControllerSettingsPersist(t *testing.T) {
	stubDiscord(t)
	c, srv := newTestController(t)
	for _, body := range []string{
		`{"action":"mode","value":"tlsrec"}`,
		`{"action":"launchDiscord","on":false}`,
		`{"action":"autoStartBypass","on":false}`,
	} {
		if code, b := call(t, srv, c, "POST", "/api/action", body, nil); code != 200 {
			t.Fatalf("%s: %d %s", body, code, b)
		}
	}
	if code, _ := call(t, srv, c, "POST", "/api/action", `{"action":"mode","value":"uydurma"}`, nil); code != 400 {
		t.Fatalf("geçersiz yöntem kabul edildi: %d", code)
	}

	data, err := os.ReadFile(filepath.Join(c.dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s appSettings
	json.Unmarshal(data, &s)
	if s.Mode != "tlsrec" || s.LaunchDiscord || s.AutoStartBypass {
		t.Fatalf("ayarlar kaydedilmedi: %+v", s)
	}
	// Yeni bir controller aynı ayarları okumalı.
	if c2 := newController(c.base, c.dir, c.panelAddr); c2.settings != s {
		t.Fatalf("ayarlar yüklenmedi: %+v", c2.settings)
	}
}

// TestBrowserBackup, gecit'teki hatanın tersini doğrular: kendi PAC adresimiz
// asla "orijinal" diye yedeklenmez ve kaldırınca eski ayar geri gelir.
func TestBrowserBackup(t *testing.T) {
	store := map[string]string{}
	oldS, oldR, oldW, oldC := pacSupported, pacRead, pacWrite, pacClear
	pacSupported = func() bool { return true }
	pacRead = func() (string, bool) { return store["url"], true }
	pacWrite = func(u string) error { store["url"] = u; return nil }
	pacClear = func() error { delete(store, "url"); return nil }
	t.Cleanup(func() { pacSupported, pacRead, pacWrite, pacClear = oldS, oldR, oldW, oldC })

	c, _ := newTestController(t)

	// 1) Önceden şirket PAC'i var: kur, iki kez kur, kaldır → eski PAC geri gelmeli.
	store["url"] = "http://proxy.sirket/kurumsal.pac"
	for i := 0; i < 2; i++ {
		if err := c.setBrowser(true); err != nil {
			t.Fatal(err)
		}
	}
	if !c.browserInstalled() || store["url"] != c.pacURL() {
		t.Fatalf("kurulum: %q", store["url"])
	}
	// Uygulama yeniden açıldı (yeni controller, aynı ayar klasörü).
	c = newController(c.base, c.dir, c.panelAddr)
	if err := c.setBrowser(false); err != nil {
		t.Fatal(err)
	}
	if store["url"] != "http://proxy.sirket/kurumsal.pac" {
		t.Fatalf("eski PAC geri gelmedi: %q", store["url"])
	}

	// 2) Önceden PAC yok: kur, kaldır → değer tamamen silinmeli.
	delete(store, "url")
	c.setBrowser(true)
	c.setBrowser(false)
	if _, ok := store["url"]; ok {
		t.Fatalf("PAC silinmedi: %q", store["url"])
	}

	// 3) Kendi adresimiz zaten yazılıyken (ör. çökme sonrası) kurulum yapılırsa
	//    yedek olarak bizim adresimiz kaydedilmemeli.
	store["url"] = c.pacURL()
	c.settings.BrowserBackedUp, c.settings.BrowserBackup = false, ""
	c.setBrowser(true)
	if c.settings.BrowserBackup == c.pacURL() {
		t.Fatal("kendi PAC adresimiz yedeklendi")
	}
	c.setBrowser(false)
	if _, ok := store["url"]; ok {
		t.Fatalf("kaldırma sonrası PAC kaldı: %q", store["url"])
	}
}

func TestControllerEvents(t *testing.T) {
	c, _ := newTestController(t)
	now := time.Now().UnixMilli()
	for _, e := range []event{
		{Type: "open", ID: 1, Host: "discord.com", Bypass: true, Mode: "tlsrec+split", IP: "1.2.3.4", Time: now},
		{Type: "open", ID: 2, Host: "discord.com", Bypass: true, Mode: "tlsrec+split", Time: now},
		{Type: "close", ID: 1, Host: "discord.com", Up: 500, Down: 9000, Time: now},
		{Type: "open", ID: 3, Host: "gateway.discord.gg", Bypass: true, Time: now},
		{Type: "close", ID: 3, Host: "gateway.discord.gg", Up: 517, Down: 0, Time: now},
		{Type: "fail", ID: 4, Host: "status.discord.com", Bypass: true, Error: "zaman aşımı", Time: now},
		{Type: "open", ID: 5, Host: "media.tenor.co", Time: now},
		{Type: "close", ID: 5, Host: "media.tenor.co", Error: "bağlantı sıfırlandı", Time: now},
	} {
		c.handleEvent(e)
	}
	h := c.hosts
	if d := h["discord.com"]; d.Total != 2 || d.Active != 1 || d.Down != 9000 || d.IP != "1.2.3.4" || d.Errors != 0 {
		t.Errorf("discord.com: %+v", *d)
	}
	if g := h["gateway.discord.gg"]; g.NoReply != 1 || g.Active != 0 {
		t.Errorf("gateway (yanıtsız): %+v", *g)
	}
	if s := h["status.discord.com"]; s.Errors != 1 || !strings.Contains(s.LastError, "zaman aşımı") {
		t.Errorf("status: %+v", *s)
	}
	if m := h["media.tenor.co"]; m.Bypass || m.Errors != 1 {
		t.Errorf("tenor: %+v", *m)
	}

	c.clear()
	if _, ok := c.hosts["media.tenor.co"]; ok || c.hosts["discord.com"].Active != 1 {
		t.Errorf("temizle: kapalı hostlar silinmeli, açık olan kalmalı: %v", c.hosts)
	}
}

// TestToggleClosesTunnels, bypass kapatılınca açık tünellerin kesildiğini ve
// yeniden açılınca aynı portta çalıştığını doğrular.
func TestToggleClosesTunnels(t *testing.T) {
	calls := stubDiscord(t)
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer tlsSrv.Close()

	c, _ := newTestController(t)
	c.settings.Mode = "tlsrec+split" // ağ testi yapmasın
	c.settings.LaunchDiscord = true  // sahte başlatıcı çağrılmalı

	waitState := func(want string) {
		t.Helper()
		for i := 0; i < 100; i++ {
			c.mu.Lock()
			st := c.state
			c.mu.Unlock()
			if st == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("durum %q olmadı", want)
	}

	c.start()
	waitState("running")
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", c.base.port)

	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	target := tlsSrv.Listener.Addr().String()
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(conn)
	if line, _ := br.ReadString('\n'); !strings.Contains(line, "200") {
		t.Fatalf("CONNECT yanıtı: %q", line)
	}
	br.ReadString('\n')

	c.stop()
	waitState("stopped")
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := br.ReadByte(); err == nil {
		t.Fatal("bypass kapandıktan sonra tünel açık kaldı")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("tünel kapatılmadı (zaman aşımı)")
	}
	if _, err := net.DialTimeout("tcp", proxyAddr, time.Second); err == nil {
		t.Fatal("kapalıyken proxy portu hâlâ dinliyor")
	}

	// Yeniden aç: aynı port, gerçek bir istek geçmeli.
	c.start()
	waitState("running")
	pool := x509.NewCertPool()
	pool.AddCert(tlsSrv.Certificate())
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		Proxy:           http.ProxyURL(mustURL("http://" + proxyAddr)),
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}
	resp, err := client.Get("https://" + target + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "ok" {
		t.Fatalf("yanıt %q", b)
	}

	// Her başlatmada (zorlamadan) Discord başlatıcısı çağrılır; "zaten proxy
	// ile çalışıyorsa dokunma" kararı başlatıcının içinde verilir.
	time.Sleep(100 * time.Millisecond)
	if len(*calls) != 2 || strings.Contains((*calls)[0], "force=true") {
		t.Fatalf("Discord başlatıcı çağrıları: %v", *calls)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
