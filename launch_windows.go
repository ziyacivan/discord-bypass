package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	detachedProcess       = 0x00000008
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

type discordApp struct {
	name string // "Discord", "DiscordPTB", "DiscordCanary"
	dir  string // %LOCALAPPDATA%\Discord (Update.exe burada)
	exe  string // "Discord.exe"
}

func findDiscord(hint string) (*discordApp, error) {
	var cands []string
	if hint != "" {
		h := hint
		if strings.EqualFold(filepath.Ext(h), ".exe") {
			h = filepath.Dir(h)
		}
		// app-1.0.x\Discord.exe verilmişse bir üst klasör Update.exe'yi içerir.
		cands = []string{h, filepath.Dir(h)}
	} else {
		base := os.Getenv("LOCALAPPDATA")
		for _, n := range []string{"Discord", "DiscordPTB", "DiscordCanary"} {
			cands = append(cands, filepath.Join(base, n))
		}
	}
	for _, dir := range cands {
		if fileExists(filepath.Join(dir, "Update.exe")) {
			name := filepath.Base(dir)
			return &discordApp{name: name, dir: dir, exe: name + ".exe"}, nil
		}
	}
	return nil, errors.New(`Discord bulunamadı (%LOCALAPPDATA%\Discord); -app ile yolunu verin`)
}

func (a *discordApp) running() bool {
	for _, name := range processNames() {
		if strings.EqualFold(name, a.exe) {
			return true
		}
	}
	return false
}

// usingProxy, Discord'u bu oturumda aynı proxy ile biz başlattıysak true döner.
func (a *discordApp) usingProxy(proxyURL string) bool {
	v, _ := launchedProxy.Load().(string)
	return v == proxyURL
}

func (a *discordApp) quit() error {
	cmd := exec.Command("taskkill", "/IM", a.exe, "/T", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	cmd.Run()
	if waitUntil(10*time.Second, func() bool { return !a.running() }) {
		time.Sleep(500 * time.Millisecond)
		return nil
	}
	return errors.New("Discord kapatılamadı; sistem tepsisinden çıkıp tekrar deneyin")
}

// start, en yeni app-* klasöründeki Discord.exe'yi doğrudan başlatır;
// kısayoldaki "Update.exe --processStart" da aynı exe'yi çalıştırır.
// Update.exe kullanılmaz çünkü ortam değişkenlerini Discord'a aktarmıyor.
// Discord'un güncelleyicisi (Rust/reqwest) proxy'yi --proxy-server'dan değil
// HTTPS_PROXY'den okuduğu için Update.exe ile açılan Discord "Update failed"
// döngüsünde kalıyordu. Update.exe yalnızca app-* bulunamazsa kullanılır.
func (a *discordApp) start(proxyURL string) error {
	var cmd *exec.Cmd
	if exe := a.latestExe(); exe != "" {
		cmd = exec.Command(exe, "--proxy-server="+proxyURL)
		cmd.Dir = filepath.Dir(exe)
	} else if upd := filepath.Join(a.dir, "Update.exe"); fileExists(upd) {
		cmd = exec.Command(upd, "--processStart", a.exe, "--process-start-args=--proxy-server="+proxyURL)
	} else {
		return fmt.Errorf("%s içinde %s ya da Update.exe bulunamadı", a.dir, a.exe)
	}
	log.Printf("başlatılıyor: %s", cmd.Path)
	cmd.Env = append(os.Environ(), proxyEnv(proxyURL)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func (a *discordApp) latestExe() string { return latestAppExe(a.dir, a.exe) }

// manualHint, PowerShell'de çalışan elle başlatma komutu. HTTPS_PROXY,
// Discord'un güncelleyicisi için gerekir.
func manualHint(proxyURL string) string {
	return `$env:HTTPS_PROXY='` + proxyURL + `'; & (Get-ChildItem "$env:LOCALAPPDATA\Discord\app-*\Discord.exe" | Sort-Object LastWriteTime | Select-Object -Last 1).FullName --proxy-server=` + proxyURL
}
