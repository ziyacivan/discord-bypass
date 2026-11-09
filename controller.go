package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// controller, tepsi uygulamasının platformdan bağımsız kısmıdır: bypass'ı
// açıp kapatır, ayarları saklar, bağlantı istatistiklerini tutar ve
// 127.0.0.1 üzerindeki panele (HTML + JSON API) hizmet verir.

//go:embed web/panel.html
var panelHTML []byte

const defaultPanelPort = 8897

// discordLauncher, testlerde gerçek Discord'a dokunmamak için değiştirilebilir.
var discordLauncher = restartDiscord

// Sistem PAC ayarına erişim; testlerde bellek içi sahteleriyle değiştirilir.
var (
	pacSupported = browserSupported
	pacRead      = readPAC
	pacWrite     = writePAC
	pacClear     = clearPAC
)

type hostStat struct {
	Host      string `json:"host"`
	Bypass    bool   `json:"bypass"`
	Mode      string `json:"mode"`
	IP        string `json:"ip,omitempty"`
	Total     int    `json:"total"`
	Active    int    `json:"active"`
	Errors    int    `json:"errors"`
	NoReply   int    `json:"noReply"`
	Up        int64  `json:"up"`
	Down      int64  `json:"down"`
	LastSeen  int64  `json:"lastSeen"` // unix ms
	LastError string `json:"lastError,omitempty"`
}

type clientApp struct {
	Name        string `json:"name"`
	Connections int    `json:"connections"`
}

type appSettings struct {
	Mode            string `json:"mode"`
	LaunchDiscord   bool   `json:"launchDiscord"`
	AutoStartBypass bool   `json:"autoStartBypass"`
	// Tarayıcı desteği kurulmadan önceki PAC adresi. Kendi adresimiz asla
	// yedek olarak kaydedilmez.
	BrowserBackup   string `json:"browserBackup,omitempty"`
	BrowserBackedUp bool   `json:"browserBackedUp,omitempty"`
}

func defaultSettings() appSettings {
	return appSettings{Mode: "auto", LaunchDiscord: true, AutoStartBypass: true}
}

type controller struct {
	base      config
	dir       string
	panelAddr string
	token     string
	pac       string

	mu             sync.Mutex
	settings       appSettings
	state          string // stopped, starting, running, failed
	errMsg         string
	notice         string
	modeName       string
	eng            *engine
	stopAfterStart bool
	hosts          map[string]*hostStat
	open           map[uint64]string
	onChange       func()
	quit           func()

	logMu sync.Mutex
	logs  []string

	clientMu  sync.Mutex
	clients   []clientApp
	clientsAt time.Time
}

func newController(base config, dir, panelAddr string) *controller {
	b := make([]byte, 16)
	rand.Read(b)
	c := &controller{
		base:      base,
		dir:       dir,
		panelAddr: panelAddr,
		token:     hex.EncodeToString(b),
		settings:  defaultSettings(),
		state:     "stopped",
		hosts:     map[string]*hostStat{},
		open:      map[uint64]string{},
	}
	m := newMatcher(base.domains, base.all)
	c.pac = pacScript(fmt.Sprintf("127.0.0.1:%d", base.port), m.domains, base.all)
	if data, err := os.ReadFile(c.settingsPath()); err == nil {
		s := defaultSettings()
		if json.Unmarshal(data, &s) == nil {
			if _, ok := modeByName(s.Mode); !ok && s.Mode != "auto" {
				s.Mode = "auto"
			}
			c.settings = s
		}
	}
	return c
}

func (c *controller) settingsPath() string { return filepath.Join(c.dir, "settings.json") }
func (c *controller) panelURL() string     { return "http://" + c.panelAddr + "/?t=" + c.token }
func (c *controller) pacURL() string       { return "http://" + c.panelAddr + "/proxy.pac" }

func (c *controller) save() {
	c.mu.Lock()
	data, _ := json.MarshalIndent(c.settings, "", "  ")
	c.mu.Unlock()
	if err := os.WriteFile(c.settingsPath(), data, 0o600); err != nil {
		log.Printf("ayarlar kaydedilemedi: %v", err)
	}
}

// changed, tepsi simgesi ve menüsünün güncellenmesi için çağrılır. Yalnızca
// durum ya da ayar değişince çağrılır; her bağlantıda değil.
func (c *controller) changed() {
	c.mu.Lock()
	fn := c.onChange
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (c *controller) setOnChange(fn func()) {
	c.mu.Lock()
	c.onChange = fn
	c.mu.Unlock()
}

// MARK: aç/kapa

func (c *controller) start() {
	c.mu.Lock()
	if c.eng != nil || c.state == "starting" {
		c.mu.Unlock()
		return
	}
	c.state, c.errMsg, c.stopAfterStart = "starting", "", false
	cfg := c.base
	cfg.mode = c.settings.Mode
	launch := c.settings.LaunchDiscord
	c.mu.Unlock()
	c.changed()

	go func() {
		d, err := newDialer(cfg)
		var e *engine
		if err == nil {
			e, err = startEngine(cfg, d)
		}

		c.mu.Lock()
		if err != nil {
			c.state, c.errMsg = "failed", err.Error()
			c.mu.Unlock()
			log.Printf("hata: %v", err)
			c.changed()
			return
		}
		if c.stopAfterStart {
			c.state = "stopped"
			c.mu.Unlock()
			e.stop()
			c.changed()
			return
		}
		c.eng, c.state, c.modeName = e, "running", e.mode.name
		c.mu.Unlock()
		log.Printf("proxy hazır: %s (yöntem: %s)", e.proxyURL(), e.mode.name)
		c.changed()

		if launch {
			c.launchDiscord(false)
		}
	}()
}

func (c *controller) stop() {
	c.mu.Lock()
	if c.state == "starting" {
		c.stopAfterStart = true
		c.mu.Unlock()
		return
	}
	e := c.eng
	c.eng, c.state, c.modeName, c.errMsg = nil, "stopped", "", ""
	for _, h := range c.hosts {
		h.Active = 0
	}
	c.open = map[uint64]string{}
	c.mu.Unlock()
	if e != nil {
		e.stop()
		log.Printf("bypass kapatıldı")
	}
	c.changed()
}

func (c *controller) running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == "running" || c.state == "starting"
}

func (c *controller) toggle() {
	if c.running() {
		c.stop()
	} else {
		c.start()
	}
}

func (c *controller) restart() {
	if !c.running() {
		return
	}
	c.stop()
	// stop, başlatma sürüyorsa yalnızca işaret koyar; o bitene kadar bekle.
	for i := 0; i < 100 && c.running(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	c.start()
}

func (c *controller) launchDiscord(force bool) {
	c.mu.Lock()
	e := c.eng
	c.mu.Unlock()
	if e == nil {
		return
	}
	err := discordLauncher(c.base.app, e.proxyURL(), force)
	c.mu.Lock()
	if err != nil {
		c.notice = "Discord başlatılamadı: " + err.Error()
		log.Printf("Discord başlatılamadı: %v", err)
	} else {
		c.notice = ""
	}
	c.mu.Unlock()
	c.changed()
}

func (c *controller) shutdown() {
	c.stop()
}

// MARK: ayarlar

func (c *controller) setMode(m string) error {
	if _, ok := modeByName(m); !ok && m != "auto" {
		return fmt.Errorf("bilinmeyen yöntem %q", m)
	}
	c.mu.Lock()
	same := c.settings.Mode == m
	c.settings.Mode = m
	c.mu.Unlock()
	c.save()
	if !same {
		go c.restart()
	}
	c.changed()
	return nil
}

func (c *controller) setLaunchDiscord(on bool) {
	c.mu.Lock()
	c.settings.LaunchDiscord = on
	c.mu.Unlock()
	c.save()
	c.changed()
}

func (c *controller) setAutoStartBypass(on bool) {
	c.mu.Lock()
	c.settings.AutoStartBypass = on
	c.mu.Unlock()
	c.save()
	c.changed()
}

func (c *controller) setAutostart(on bool) error {
	if !autostartSupported() {
		return errors.New("bu platformda desteklenmiyor")
	}
	err := setAutostart(on)
	c.changed()
	return err
}

func (c *controller) browserInstalled() bool {
	cur, ok := pacRead()
	return ok && cur == c.pacURL()
}

// setBrowser, sistem proxy ayarına PAC adresimizi yazar ya da önceki ayarı geri yükler.
func (c *controller) setBrowser(on bool) error {
	if !pacSupported() {
		return errors.New("bu platformda desteklenmiyor")
	}
	cur, _ := pacRead()
	c.mu.Lock()
	s := c.settings
	c.mu.Unlock()

	if on {
		if cur != c.pacURL() && !s.BrowserBackedUp {
			s.BrowserBackup, s.BrowserBackedUp = cur, true
		}
		if err := pacWrite(c.pacURL()); err != nil {
			return err
		}
	} else {
		if cur == c.pacURL() {
			var err error
			if s.BrowserBackedUp && s.BrowserBackup != "" && s.BrowserBackup != c.pacURL() {
				err = pacWrite(s.BrowserBackup)
			} else {
				err = pacClear()
			}
			if err != nil {
				return err
			}
		}
		s.BrowserBackup, s.BrowserBackedUp = "", false
	}

	c.mu.Lock()
	c.settings.BrowserBackup, c.settings.BrowserBackedUp = s.BrowserBackup, s.BrowserBackedUp
	c.mu.Unlock()
	c.save()
	c.changed()
	return nil
}

func (c *controller) clear() {
	c.mu.Lock()
	for k, h := range c.hosts {
		if h.Active == 0 {
			delete(c.hosts, k)
		} else {
			*h = hostStat{Host: h.Host, Bypass: h.Bypass, Mode: h.Mode, IP: h.IP, Active: h.Active, Total: h.Active, LastSeen: h.LastSeen}
		}
	}
	c.mu.Unlock()
	c.logMu.Lock()
	c.logs = nil
	c.logMu.Unlock()
}

// MARK: olaylar

func (c *controller) handleEvent(e event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e.Type {
	case "open":
		c.open[e.ID] = e.Host
		h := c.host(e.Host)
		h.Total++
		h.Active++
		h.Bypass, h.Mode, h.LastSeen = e.Bypass, e.Mode, e.Time
		if e.IP != "" {
			h.IP = e.IP
		}
	case "close":
		name, ok := c.open[e.ID]
		delete(c.open, e.ID)
		if !ok {
			name = e.Host
		}
		h, exists := c.hosts[name]
		if !exists {
			return
		}
		if ok && h.Active > 0 {
			h.Active--
		}
		h.Up += e.Up
		h.Down += e.Down
		h.LastSeen = e.Time
		if e.Error != "" {
			h.Errors++
			h.LastError = e.Error
		} else if h.Bypass && e.Up > 0 && e.Down == 0 {
			// ClientHello gitti ama hiç yanıt gelmedi: engellenmiş olabilir.
			h.NoReply++
			h.LastError = "ClientHello gönderildi, sunucudan yanıt gelmedi"
		}
	case "fail":
		h := c.host(e.Host)
		h.Total++
		h.Errors++
		h.Bypass = e.Bypass
		h.LastSeen = e.Time
		h.LastError = "bağlanılamadı: " + e.Error
	}
}

func (c *controller) host(name string) *hostStat {
	h, ok := c.hosts[name]
	if !ok {
		h = &hostStat{Host: name}
		c.hosts[name] = h
	}
	return h
}

// logWriter, log çıktısını paneldeki "Kayıt" sekmesi için saklar.
func (c *controller) logWriter() io.Writer { return logRing{c} }

type logRing struct{ c *controller }

func (l logRing) Write(p []byte) (int, error) {
	l.c.logMu.Lock()
	defer l.c.logMu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			l.c.logs = append(l.c.logs, line)
		}
	}
	if n := len(l.c.logs); n > 300 {
		l.c.logs = append([]string(nil), l.c.logs[n-300:]...)
	}
	return len(p), nil
}

// MARK: panel

type panelState struct {
	Version   string      `json:"version"`
	Platform  string      `json:"platform"`
	State     string      `json:"state"`
	Error     string      `json:"error,omitempty"`
	Notice    string      `json:"notice,omitempty"`
	Mode      string      `json:"mode,omitempty"`
	Proxy     string      `json:"proxy"`
	Modes     []string    `json:"modes"`
	Settings  panelConfig `json:"settings"`
	Autostart struct {
		Supported bool `json:"supported"`
		Enabled   bool `json:"enabled"`
	} `json:"autostart"`
	Browser struct {
		Supported bool   `json:"supported"`
		Installed bool   `json:"installed"`
		PAC       string `json:"pac"`
	} `json:"browser"`
	Clients []clientApp `json:"clients"`
	Hosts   []hostStat  `json:"hosts"`
	Log     []string    `json:"log"`
}

type panelConfig struct {
	Mode            string `json:"mode"`
	LaunchDiscord   bool   `json:"launchDiscord"`
	AutoStartBypass bool   `json:"autoStartBypass"`
}

func (c *controller) snapshot() panelState {
	var s panelState
	s.Version = version
	s.Platform = runtime.GOOS
	s.Proxy = fmt.Sprintf("127.0.0.1:%d", c.base.port)
	s.Modes = append([]string{"auto"}, modeList()...)
	s.Autostart.Supported = autostartSupported()
	s.Autostart.Enabled = s.Autostart.Supported && autostartEnabled()
	s.Browser.Supported = pacSupported()
	s.Browser.Installed = s.Browser.Supported && c.browserInstalled()
	s.Browser.PAC = c.pacURL()

	c.mu.Lock()
	s.State, s.Error, s.Notice, s.Mode = c.state, c.errMsg, c.notice, c.modeName
	s.Settings = panelConfig{c.settings.Mode, c.settings.LaunchDiscord, c.settings.AutoStartBypass}
	s.Hosts = make([]hostStat, 0, len(c.hosts))
	for _, h := range c.hosts {
		s.Hosts = append(s.Hosts, *h)
	}
	running := c.state == "running"
	c.mu.Unlock()

	sort.Slice(s.Hosts, func(i, j int) bool {
		if s.Hosts[i].LastSeen != s.Hosts[j].LastSeen {
			return s.Hosts[i].LastSeen > s.Hosts[j].LastSeen
		}
		return s.Hosts[i].Host < s.Hosts[j].Host
	})
	s.Clients = c.proxyClients(running)

	c.logMu.Lock()
	s.Log = append([]string(nil), c.logs...)
	c.logMu.Unlock()
	return s
}

// proxyClients, proxy'ye bağlı uygulamaları en fazla 3 saniyede bir sorgular.
func (c *controller) proxyClients(running bool) []clientApp {
	if !running {
		return []clientApp{}
	}
	c.clientMu.Lock()
	defer c.clientMu.Unlock()
	if time.Since(c.clientsAt) > 3*time.Second {
		c.clients = listClients(c.base.port)
		c.clientsAt = time.Now()
	}
	if c.clients == nil {
		return []clientApp{}
	}
	return c.clients
}

type panelAction struct {
	Action string `json:"action"`
	On     bool   `json:"on"`
	Value  string `json:"value"`
}

func (c *controller) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src data:")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Write(panelHTML)
	})
	// Tarayıcılar PAC dosyasını token olmadan alabilmeli.
	mux.HandleFunc("/proxy.pac", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		w.Header().Set("Cache-Control", "no-cache")
		io.WriteString(w, c.pac)
	})
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "discord-bypass")
	})
	mux.HandleFunc("/api/state", c.requireToken(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, c.snapshot())
	}))
	mux.HandleFunc("/api/action", c.requireToken(c.handleAction))
	return c.checkHost(mux)
}

// checkHost, DNS rebinding saldırılarına karşı yalnızca 127.0.0.1/localhost
// Host başlığını kabul eder.
func (c *controller) checkHost(next http.Handler) http.Handler {
	_, port, _ := strings.Cut(c.panelAddr, ":")
	allowed := map[string]bool{c.panelAddr: true, "localhost:" + port: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "yasak", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireToken, API'yi yalnızca panel adresindeki gizli token'ı bilenlere açar.
// Başka sitelerin sayfaları bu başlığı ekleyemez (CORS ön kontrolü yapılmaz).
func (c *controller) requireToken(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Token")), []byte(c.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "geçersiz token; paneli tepsi simgesinden açın"})
			return
		}
		h(w, r)
	}
}

func (c *controller) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST application/json gerekli"})
		return
	}
	var a panelAction
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&a); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "geçersiz istek"})
		return
	}

	var err error
	switch a.Action {
	case "start":
		c.start()
	case "stop":
		c.stop()
	case "relaunch":
		go c.launchDiscord(true)
	case "clear":
		c.clear()
	case "mode":
		err = c.setMode(a.Value)
	case "launchDiscord":
		c.setLaunchDiscord(a.On)
	case "autoStartBypass":
		c.setAutoStartBypass(a.On)
	case "autostart":
		err = c.setAutostart(a.On)
	case "browser":
		err = c.setBrowser(a.On)
	case "quit":
		c.mu.Lock()
		q := c.quit
		c.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"state": "quitting"})
		if q != nil {
			go q()
		}
		return
	default:
		err = fmt.Errorf("bilinmeyen işlem %q", a.Action)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, c.snapshot())
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
