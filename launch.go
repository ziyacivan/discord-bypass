package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// launchedProxy, bu oturumda Discord'un hangi proxy adresiyle başlatıldığını
// tutar. Windows'ta başka bir sürecin komut satırını okumak zahmetli olduğu
// için "zaten proxy ile mi çalışıyor" sorusu buna bakılarak cevaplanır.
var launchedProxy atomic.Value

// launchMu, aynı anda iki yeniden başlatmanın (kullanıcı + izleyici) çakışmasını önler.
var launchMu sync.Mutex

// restartDiscord, Discord'u kapatır ve proxy ayarıyla yeniden başlatır.
// Ayar yalnızca bu Discord sürecine komut satırı argümanı olarak verilir;
// sistemde kalıcı hiçbir şey değişmez. force=false ise zaten bu proxy ile
// çalışan Discord'a dokunulmaz.
func restartDiscord(hint, proxyURL string, force bool) error {
	launchMu.Lock()
	defer launchMu.Unlock()
	a, err := findDiscord(hint)
	if err != nil {
		return err
	}
	if a.running() {
		if !force && a.usingProxy(proxyURL) {
			log.Printf("%s zaten bu proxy ile çalışıyor.", a.name)
			return nil
		}
		log.Printf("%s açık; proxy ile yeniden başlatmak için kapatılıyor...", a.name)
		if err := a.quit(); err != nil {
			return err
		}
	}
	if err := a.start(proxyURL); err != nil {
		return fmt.Errorf("%s başlatılamadı: %w", a.name, err)
	}
	launchedProxy.Store(proxyURL)
	log.Printf("%s proxy ile başlatıldı.", a.name)
	return nil
}

// watchDiscord, Discord'un proxy'siz açılıp açılmadığını izler. Discord yeni
// bir sürüm kurduğunda kendini argümansız yeniden başlatır; bu durumda
// --proxy-server kaybolur ve güncelleyici "Checking for updates" ekranında
// kalır. Proxy'siz çalışan Discord art arda iki kontrolde görülürse relaunch
// çağrılır. Kapalı Discord'a dokunulmaz; döngüye girmemek için iki yeniden
// başlatma arasında en az 30 sn beklenir.
func watchDiscord(hint, proxyURL string, stop <-chan struct{}, relaunch func()) {
	a, err := findDiscord(hint)
	if err != nil {
		return
	}
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	misses := 0
	var last time.Time
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if v, _ := launchedProxy.Load().(string); v != proxyURL {
			continue // Discord'u bu oturumda biz başlatmadık
		}
		if !launchMu.TryLock() {
			misses = 0 // başka bir başlatma sürüyor
			continue
		}
		bad := a.running() && !a.usingProxy(proxyURL)
		launchMu.Unlock()
		if !bad {
			misses = 0
			continue
		}
		if misses++; misses < 2 || time.Since(last) < 30*time.Second {
			continue
		}
		misses, last = 0, time.Now()
		log.Printf("%s proxy'siz yeniden açılmış (büyük olasılıkla güncelleme sonrası); proxy ile yeniden başlatılıyor.", a.name)
		relaunch()
	}
}

// proxyEnv, Chromium dışındaki bileşenler (ör. güncelleyici) için proxy
// ortam değişkenleri. Yalnızca başlatılan Discord sürecine verilir.
func proxyEnv(proxyURL string) []string {
	noProxy := "localhost,127.0.0.1,::1"
	return []string{
		"HTTPS_PROXY=" + proxyURL, "https_proxy=" + proxyURL,
		"HTTP_PROXY=" + proxyURL, "http_proxy=" + proxyURL,
		"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
	}
}

func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return cond()
}

// latestAppExe, Windows'taki Discord kurulum klasöründe (dir) en yeni
// app-* sürümünün exe'sini döndürür; Update.exe de bunu çalıştırır.
func latestAppExe(dir, exe string) string {
	dirs, _ := filepath.Glob(filepath.Join(dir, "app-*"))
	sort.Slice(dirs, func(i, j int) bool { return versionLess(dirs[i], dirs[j]) })
	for i := len(dirs) - 1; i >= 0; i-- {
		if p := filepath.Join(dirs[i], exe); fileExists(p) {
			return p
		}
	}
	return ""
}

// versionLess, "app-1.0.9010" gibi klasörleri sürüm numarasına göre karşılaştırır.
func versionLess(a, b string) bool {
	pa := strings.Split(strings.TrimPrefix(filepath.Base(a), "app-"), ".")
	pb := strings.Split(strings.TrimPrefix(filepath.Base(b), "app-"), ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		var x, y int
		fmt.Sscan(pa[i], &x)
		fmt.Sscan(pb[i], &y)
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
