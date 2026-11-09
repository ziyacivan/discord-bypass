package main

import (
	"encoding/binary"
	"net"
	"strings"
	"time"
)

// mode, TLS ClientHello'nun DPI'ı şaşırtmak için nasıl gönderileceğini tanımlar.
// Hiçbiri root/yönetici yetkisi gerektirmez.
type mode struct {
	name string
	// tlsrec: ClientHello, SNI'nin ortasından ikiye bölünüp iki ayrı TLS
	// kaydı olarak gönderilir. Sunucular bunu standart gereği birleştirir;
	// yalnızca ilk kayda bakan DPI host adının tamamını göremez.
	tlsrec bool
	// split: Parçalar ayrı TCP segmentleri olarak gönderilir; segmentleri
	// birleştirmeyen DPI host adının tamamını göremez.
	split bool
	// disorder: İlk parça TTL=1 ile gönderilir ve ilk router'da düşer. DPI
	// önce ikinci parçayı görür; çekirdek ilk parçayı birkaç ms sonra normal
	// TTL ile yeniden iletir. Daha yavaştır, diğerleri çalışmazsa kullanılır.
	disorder bool
}

// modes, otomatik seçimde denenme sırasıyla listelenir.
var modes = []mode{
	{name: "tlsrec+split", tlsrec: true, split: true},
	{name: "tlsrec", tlsrec: true},
	{name: "split", split: true},
	{name: "disorder", tlsrec: true, split: true, disorder: true},
	{name: "none"},
}

func modeByName(name string) (mode, bool) {
	for _, m := range modes {
		if m.name == name {
			return m, true
		}
	}
	return mode{}, false
}

func modeList() []string {
	names := make([]string, len(modes))
	for i, m := range modes {
		names[i] = m.name
	}
	return names
}

func modeNames() string { return strings.Join(modeList(), ", ") }

func (m mode) active() bool { return m.tlsrec || m.split }

// Parçaların ayrı segmentlerde gitmesi için aralarında beklenen süre.
const splitDelay = 5 * time.Millisecond

// splitHello, ClientHello'yu seçilen yönteme göre gönderilecek parçalara ayırır.
// SNI bulunamazsa veri olduğu gibi tek parça döner.
func splitHello(hello []byte, m mode) [][]byte {
	if !m.active() || len(hello) < 5 {
		return [][]byte{hello}
	}
	off, n, ok := findSNI(hello)
	if !ok {
		return [][]byte{hello}
	}
	// Host adını ortasından kes: hiçbir parça "discord" kelimesinin tamamını içermesin.
	cut := off + max(1, n/2)
	recEnd := 5 + int(binary.BigEndian.Uint16(hello[3:5]))
	if recEnd > len(hello) || cut <= 5 || cut >= recEnd {
		return [][]byte{hello}
	}

	if !m.tlsrec {
		return [][]byte{hello[:cut], hello[cut:]}
	}

	first := tlsRecord(hello[1:3], hello[5:cut])
	second := tlsRecord(hello[1:3], hello[cut:recEnd])
	second = append(second, hello[recEnd:]...)
	if m.split {
		return [][]byte{first, second}
	}
	return [][]byte{append(first, second...)}
}

func tlsRecord(version, payload []byte) []byte {
	r := make([]byte, 5, 5+len(payload))
	r[0] = 0x16 // handshake
	r[1], r[2] = version[0], version[1]
	binary.BigEndian.PutUint16(r[3:5], uint16(len(payload)))
	return append(r, payload...)
}

// writeHello, ClientHello'yu seçilen yöntemle sunucuya yazar.
func writeHello(conn net.Conn, hello []byte, m mode) error {
	parts := splitHello(hello, m)
	tcp, _ := conn.(*net.TCPConn)
	if tcp != nil && len(parts) > 1 {
		tcp.SetNoDelay(true)
	}

	for i, p := range parts {
		last := i == len(parts)-1
		var err error
		if i == 0 && m.disorder && !last && tcp != nil {
			err = writeLowTTL(tcp, p)
		} else {
			_, err = conn.Write(p)
		}
		if err != nil {
			return err
		}
		if !last {
			time.Sleep(splitDelay)
		}
	}
	return nil
}

// writeLowTTL, p'yi TTL=1 ile gönderir ve ardından eski TTL'i geri yükler.
// TTL okunamaz ya da değiştirilemezse veri normal şekilde gönderilir.
func writeLowTTL(c *net.TCPConn, p []byte) error {
	v6 := isIPv6(c.RemoteAddr())
	old, err := getTTL(c, v6)
	if err != nil || setTTL(c, v6, 1) != nil {
		_, err = c.Write(p)
		return err
	}
	_, werr := c.Write(p)
	// Segment kablodan çıkmadan TTL geri alınırsa yöntem işe yaramaz.
	time.Sleep(splitDelay)
	if err := setTTL(c, v6, old); err != nil && werr == nil {
		// TTL 1'de kalırsa bağlantı zaten çalışmaz; kapatılması için hata dön.
		return err
	}
	return werr
}

func isIPv6(a net.Addr) bool {
	ta, ok := a.(*net.TCPAddr)
	return ok && ta.IP.To4() == nil
}
