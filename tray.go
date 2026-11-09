package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// cmdTray, tepsi uygulamasını çalıştırır: paneli 127.0.0.1:panelPort'ta
// sunar, ayara göre bypass'ı başlatır ve tepsi simgesini gösterir (Windows).
// Diğer platformlarda simge olmadan çalışır (geliştirme ve test için).
func cmdTray(args []string) error {
	var c config
	fs := newFlagSet("tray", &c)
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := c.configDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		dir = filepath.Join(base, "discord-bypass")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	panelAddr := fmt.Sprintf("127.0.0.1:%d", c.panelPort)
	ln, err := net.Listen("tcp", panelAddr)
	if err != nil {
		// Uygulama zaten açıksa ikinci kopya yerine onun panelini aç.
		if url, ok := existingInstance(dir, panelAddr); ok {
			if !c.autostart {
				return openPanelWindow(url)
			}
			return nil
		}
		return fmt.Errorf("panel portu %s kullanımda; başka bir program kullanıyor olabilir: %w", panelAddr, err)
	}

	ctrl := newController(c, dir, panelAddr)
	setupTrayLog(ctrl, dir)
	setEventHandler(ctrl.handleEvent)

	srv := &http.Server{Handler: ctrl.handler(), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	urlFile := filepath.Join(dir, "panel.url")
	os.WriteFile(urlFile, []byte(ctrl.panelURL()), 0o600)
	defer os.Remove(urlFile)

	log.Printf("discord-bypass %s başladı (panel: http://%s)", version, panelAddr)
	healAutostart()
	if ctrl.settings.AutoStartBypass {
		ctrl.start()
	}
	if !c.autostart {
		openPanelWindow(ctrl.panelURL())
	}

	runTray(ctrl)
	ctrl.shutdown()
	log.Printf("uygulama kapatıldı")
	return nil
}

// existingInstance, aynı kullanıcıda çalışan bir kopya varsa panel adresini döndürür.
func existingInstance(dir, addr string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "panel.url"))
	if err != nil {
		return "", false
	}
	cl := http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get("http://" + addr + "/api/ping")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if string(b) != "discord-bypass" {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

// setupTrayLog, log çıktısını panelin "Kayıt" sekmesine ve bir dosyaya yönlendirir.
func setupTrayLog(c *controller, dir string) {
	path := filepath.Join(dir, "discord-bypass.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 1<<20 {
		os.Rename(path, path+".old")
	}
	writers := []io.Writer{c.logWriter()}
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		writers = append(writers, f)
	}
	if !isGUI() {
		writers = append(writers, os.Stderr)
	}
	log.SetOutput(io.MultiWriter(writers...))
}

func sortClients(counts map[string]int) []clientApp {
	out := make([]clientApp, 0, len(counts))
	for name, n := range counts {
		out = append(out, clientApp{Name: name, Connections: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Connections != out[j].Connections {
			return out[i].Connections > out[j].Connections
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
