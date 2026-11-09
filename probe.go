package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"syscall"
	"time"
)

// BTK'nın engelli alan adları için DNS'te döndürdüğü uyarı sayfası adresi.
var btkBlockPage = net.ParseIP("195.175.254.2")

var defaultProbeHosts = []string{"discord.com", "gateway.discord.gg", "cdn.discordapp.com"}

// probeOnce, host'a DoH ile çözülen IP üzerinden, verilen yöntemle TLS
// bağlantısı kurar ve bir HTTP yanıtı bekler. Sertifika doğrulandığı için
// başarı, gerçek Discord sunucusuna ulaşıldığı anlamına gelir.
func probeOnce(ctx context.Context, d *dialer, host string, m mode) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	ips, err := d.doh.lookup(ctx, host)
	if err != nil {
		return err
	}
	raw, err := d.dialIPs(ctx, ips, "443")
	if err != nil {
		return err
	}
	defer raw.Close()
	if dl, ok := ctx.Deadline(); ok {
		raw.SetDeadline(dl)
	}

	tc := tls.Client(&helloConn{Conn: raw, m: m}, &tls.Config{ServerName: host, NextProtos: []string{"http/1.1"}})
	if err := tc.HandshakeContext(ctx); err != nil {
		return err
	}
	fmt.Fprintf(tc, "HEAD / HTTP/1.1\r\nHost: %s\r\nUser-Agent: discord-bypass/%s\r\nConnection: close\r\n\r\n", host, version)
	line, err := bufio.NewReader(tc).ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "HTTP/") {
		return fmt.Errorf("beklenmeyen yanıt: %q", strings.TrimSpace(line))
	}
	return nil
}

// helloConn, ilk Write çağrısını (ClientHello) writeHello'dan geçirir.
type helloConn struct {
	net.Conn
	m    mode
	done bool
}

func (c *helloConn) Write(b []byte) (int, error) {
	if c.done {
		return c.Conn.Write(b)
	}
	c.done = true
	if err := writeHello(c.Conn, b, c.m); err != nil {
		return 0, err
	}
	return len(b), nil
}

// autoMode, yöntemleri sırayla dener ve discord.com'a ulaşan ilkini seçer.
func autoMode(d *dialer) mode {
	log.Printf("en uygun yöntem aranıyor...")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	_, err := d.doh.lookup(ctx, "discord.com")
	cancel()
	if err != nil {
		// Ağ yoksa her yöntemi ayrı ayrı beklemenin anlamı yok.
		log.Printf("uyarı: DoH'a ulaşılamadı (%s); %s ile devam ediliyor", shortErr(err), modes[0].name)
		return modes[0]
	}
	for _, m := range modes {
		start := time.Now()
		err := probeOnce(context.Background(), d, "discord.com", m)
		ms := time.Since(start).Milliseconds()
		emit(event{Type: "probe", Mode: m.name, OK: err == nil, Ms: ms, Error: probeErr(err)})
		if err == nil {
			log.Printf("yöntem seçildi: %s (%d ms)", m.name, ms)
			return m
		}
		log.Printf("  %s çalışmadı: %s", m.name, shortErr(err))
	}
	log.Printf("uyarı: hiçbir yöntem doğrulanamadı (internet bağlantısı var mı?); %s ile devam ediliyor", modes[0].name)
	return modes[0]
}

func cmdProbe(args []string) error {
	var c config
	fs := newFlagSet("probe", &c)
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := newDialer(c)
	if err != nil {
		return err
	}
	hosts := fs.Args()
	if len(hosts) == 0 {
		hosts = defaultProbeHosts
	}
	ctx := context.Background()

	fmt.Println("1) DNS: sistem DNS'i ile DoH karşılaştırması")
	for _, h := range hosts {
		sys, serr := net.DefaultResolver.LookupIP(ctx, "ip4", h)
		doh, derr := d.doh.lookup(ctx, h)
		fmt.Printf("   %-22s sistem: %-34s DoH: %s\n", h, ipList(sys, serr), ipList(doh, derr))
		fmt.Printf("   %-22s → %s\n", "", dnsVerdict(sys, serr, doh))
	}

	fmt.Println("\n2) TLS: DoH ile bulunan IP'ye her yöntemle bağlantı")
	working := map[string]int{}
	for _, h := range hosts {
		fmt.Printf("   %s\n", h)
		for _, m := range modes {
			start := time.Now()
			if err := probeOnce(ctx, d, h, m); err != nil {
				fmt.Printf("     ✗ %-13s %s\n", m.name, shortErr(err))
				continue
			}
			working[m.name]++
			fmt.Printf("     ✓ %-13s %d ms\n", m.name, time.Since(start).Milliseconds())
		}
	}

	fmt.Println()
	if working["none"] == len(hosts) {
		fmt.Println("SNI engeli görünmüyor: doğru IP'ye gitmek (DoH) yeterli.")
	} else if working["none"] == 0 {
		fmt.Println("SNI tabanlı DPI engeli var: ClientHello parçalanmadan bağlantı kurulamıyor.")
	}
	for _, m := range modes {
		if working[m.name] == len(hosts) {
			fmt.Printf("Önerilen yöntem: %s (otomatik mod da bunu seçer)\n", m.name)
			return nil
		}
	}
	fmt.Println("Tüm hostlarda çalışan bir yöntem bulunamadı. -iface ile fiziksel arayüzü deneyin ya da VPN/TUN yazılımlarını kapatın.")
	return nil
}

func probeErr(err error) string {
	if err == nil {
		return ""
	}
	return shortErr(err)
}

func dnsVerdict(sys []net.IP, serr error, doh []net.IP) string {
	if serr != nil {
		return "sistem DNS'i yanıt vermedi (DNS ayarları bozuk olabilir)"
	}
	for _, ip := range sys {
		if ip.Equal(btkBlockPage) {
			return "ZEHİRLİ: sistem DNS'i BTK uyarı sayfasını döndürüyor"
		}
	}
	for _, a := range sys {
		for _, b := range doh {
			if a.Equal(b) {
				return "tutarlı"
			}
		}
	}
	return "farklı sonuç (CDN farkı ya da zehirleme olabilir)"
}

func ipList(ips []net.IP, err error) string {
	if err != nil {
		return "hata"
	}
	s := make([]string, 0, 2)
	for i, ip := range ips {
		if i == 2 {
			s = append(s, "…")
			break
		}
		s = append(s, ip.String())
	}
	return strings.Join(s, " ")
}

func shortErr(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNRESET):
		return "bağlantı sıfırlandı (RST, muhtemelen DPI)"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "zaman aşımı (paketler düşürülüyor olabilir)"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "bağlantı kapatıldı (EOF, muhtemelen DPI)"
	}
	s := err.Error()
	if len(s) > 90 {
		s = s[:90] + "…"
	}
	return s
}
