// discord-bypass, Discord'a erişim için sistem ayarlarına dokunmayan hafif
// bir yerel proxy'dir. DNS, route ya da ağ arayüzü değiştirmez; root veya
// yönetici yetkisi gerektirmez. Program kapandığında geri alınacak bir şey
// kalmaz.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

var version = "dev"

// defaultCmd, argümansız çalıştırıldığında hangi komutun çalışacağıdır.
// Windows tepsi uygulaması "-X main.defaultCmd=tray" ile derlenir.
var defaultCmd = "run"

const defaultPort = 8899

type config struct {
	port    int
	mode    string
	doh     string
	domains string
	all     bool
	iface   string
	verbose bool
	launch  bool
	app     string
	ui      bool

	// tepsi uygulaması
	panelPort int
	configDir string
	autostart bool
}

func main() {
	setupConsole()
	log.SetFlags(log.Ltime)

	cmd, args := defaultCmd, os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	var err error
	switch cmd {
	case "run":
		err = cmdRun(args)
	case "probe":
		err = cmdProbe(args)
	case "pac":
		err = cmdPAC(args)
	case "tray":
		err = cmdTray(args)
	case "version":
		fmt.Println("discord-bypass", version)
	case "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "bilinmeyen komut: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Printf("hata: %v", err)
		emit(event{Type: "error", Error: err.Error()})
		if cmd == "tray" {
			showError(err.Error())
		}
		pauseIfOwnConsole()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `discord-bypass: Discord erişimi için hafif yerel proxy

Kullanım:
  discord-bypass [seçenekler]          proxy'yi başlatır ve Discord'u proxy ile açar
  discord-bypass probe [host...]       engel türünü ve çalışan yöntemleri test eder
  discord-bypass pac                   tarayıcılar için PAC dosyasını yazdırır
  discord-bypass tray                  tepsi uygulaması (Windows; diğerlerinde yalnızca panel)
  discord-bypass version

Seçenekler için: discord-bypass -h  ya da  discord-bypass probe -h
`)
}

func newFlagSet(name string, c *config) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&c.mode, "mode", "auto", "engel aşma yöntemi: auto, "+modeNames())
	fs.StringVar(&c.doh, "doh", "cloudflare,google", "DoH sunucuları: cloudflare, google, quad9 ya da https:// adresleri (virgülle, sırayla denenir)")
	fs.StringVar(&c.domains, "domains", "", "Discord listesine eklenecek alan adları (virgülle)")
	fs.BoolVar(&c.all, "all", false, "proxy'den geçen tüm alan adlarına uygula (yalnızca Discord yerine)")
	fs.StringVar(&c.iface, "iface", "", "giden bağlantıları bu ağ arayüzüne bağla, ör. en0 (gelişmiş; VPN/TUN ile çakışmada)")
	fs.BoolVar(&c.verbose, "v", false, "her bağlantıyı logla")
	if name == "run" {
		fs.IntVar(&c.port, "port", defaultPort, "yerel proxy portu (127.0.0.1)")
		fs.BoolVar(&c.launch, "launch", true, "Discord'u proxy ile (yeniden) başlat")
		fs.StringVar(&c.app, "app", "", "Discord'un yolu (boşsa otomatik bulunur)")
		fs.BoolVar(&c.ui, "ui", false, "menü çubuğu uygulaması için: olayları JSON satırları olarak stdout'a yazar, stdin'den komut okur, stdin kapanınca çıkar")
	}
	if name == "tray" {
		fs.IntVar(&c.port, "port", defaultPort, "yerel proxy portu (127.0.0.1)")
		fs.StringVar(&c.app, "app", "", "Discord'un yolu (boşsa otomatik bulunur)")
		fs.IntVar(&c.panelPort, "panel-port", defaultPanelPort, "panel ve PAC dosyası için port (127.0.0.1)")
		fs.StringVar(&c.configDir, "config-dir", "", "ayar ve kayıt klasörü (boşsa kullanıcı ayar klasörü)")
		fs.BoolVar(&c.autostart, "autostart", false, "oturum açılışında başlatıldı: paneli açma")
	}
	return fs
}

func cmdRun(args []string) error {
	var c config
	fs := newFlagSet("run", &c)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if c.ui {
		enableEvents(os.Stdout)
	}
	d, err := newDialer(c)
	if err != nil {
		return err
	}

	e, err := startEngine(c, d)
	if err != nil {
		return err
	}
	defer e.stop()

	proxyURL := e.proxyURL()
	log.Printf("proxy hazır: %s  (yöntem: %s)", proxyURL, e.mode.name)
	emit(event{Type: "ready", Proxy: proxyURL, Mode: e.mode.name})

	launch := func(force bool) {
		err := restartDiscord(c.app, proxyURL, force)
		if err != nil {
			log.Printf("Discord otomatik başlatılamadı: %v", err)
			log.Printf("elle başlatmak için: %s", manualHint(proxyURL))
		}
		emit(event{Type: "discord", OK: err == nil, Error: errString(err)})
	}
	if c.launch {
		launch(false)
	} else if !c.ui {
		log.Printf("Discord'u şöyle başlatın: %s", manualHint(proxyURL))
	}

	done := make(chan struct{})
	if c.ui {
		go readCommands(func(cmd string) {
			if cmd == "relaunch" {
				go launch(true)
			}
		}, done)
	} else {
		log.Printf("tarayıcıda kullanmak için PAC adresi: %s/proxy.pac", proxyURL)
		log.Printf("çalışıyor; durdurmak için Ctrl+C (bu pencere açık kalmalı)")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-done:
	}
	log.Printf("kapatılıyor. Sistem ayarı değiştirilmediği için geri alınacak bir şey yok.")
	if !c.ui {
		log.Printf("not: proxy ile açılan Discord artık bağlanamaz; normal kullanım için Discord'u kapatıp yeniden açın.")
	}
	return nil
}

// cmdPAC, Discord alan adlarını 127.0.0.1:port'a yönlendiren PAC dosyasını
// stdout'a yazar. Menü çubuğu uygulaması tarayıcılar için bunu sunar.
func cmdPAC(args []string) error {
	fs := flag.NewFlagSet("pac", flag.ContinueOnError)
	port := fs.Int("port", defaultPort, "proxy portu")
	domains := fs.String("domains", "", "Discord listesine eklenecek alan adları (virgülle)")
	all := fs.Bool("all", false, "tüm alan adlarını proxy'ye yönlendir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Print(pacScript(fmt.Sprintf("127.0.0.1:%d", *port), newMatcher(*domains, *all).domains, *all))
	return nil
}

func pickMode(name string, d *dialer) (mode, error) {
	if name == "auto" {
		return autoMode(d), nil
	}
	m, ok := modeByName(name)
	if !ok {
		return mode{}, fmt.Errorf("bilinmeyen yöntem %q; seçenekler: auto, %s", name, modeNames())
	}
	return m, nil
}
