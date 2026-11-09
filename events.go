package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// event, -ui modunda menü çubuğu uygulamasına stdout üzerinden satır satır
// JSON olarak gönderilir.
type event struct {
	Type   string `json:"type"` // ready, probe, open, close, fail, discord, error
	Time   int64  `json:"t"`
	ID     uint64 `json:"id,omitempty"`
	Host   string `json:"host,omitempty"`
	Port   string `json:"port,omitempty"`
	IP     string `json:"ip,omitempty"`
	Bypass bool   `json:"bypass,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Up     int64  `json:"up,omitempty"`
	Down   int64  `json:"down,omitempty"`
	Ms     int64  `json:"ms,omitempty"`
	OK     bool   `json:"ok,omitempty"`
	Error  string `json:"error,omitempty"`
	Proxy  string `json:"proxy,omitempty"`
}

type eventSink struct {
	mu sync.Mutex
	fn func(event)
}

var events eventSink

// setEventHandler, olayları alacak fonksiyonu ayarlar (nil: kapalı). Tepsi
// uygulaması olayları süreç içinde bu yolla alır.
func setEventHandler(fn func(event)) {
	events.mu.Lock()
	events.fn = fn
	events.mu.Unlock()
}

// enableEvents, olayları w'ye JSON satırları olarak yazar (menü çubuğu uygulaması için).
func enableEvents(w io.Writer) {
	var mu sync.Mutex
	enc := json.NewEncoder(w)
	setEventHandler(func(e event) {
		mu.Lock()
		enc.Encode(e)
		mu.Unlock()
	})
}

func emit(e event) {
	events.mu.Lock()
	fn := events.fn
	events.mu.Unlock()
	if fn == nil {
		return
	}
	e.Time = time.Now().UnixMilli()
	fn(e)
}

// readCommands, stdin'den satır satır komut okur. stdin kapandığında (menü
// uygulaması kapandı ya da çöktü) done kapatılır ve program sonlanır.
func readCommands(handle func(cmd string), done chan<- struct{}) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if cmd := strings.TrimSpace(sc.Text()); cmd != "" {
			handle(cmd)
		}
	}
	close(done)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
