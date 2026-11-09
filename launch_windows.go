package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// start, Discord'u kısayolun yaptığı gibi Update.exe üzerinden başlatır ve
// proxy argümanını --process-start-args ile iletir. Update.exe yoksa en yeni
// app-* klasöründeki Discord.exe doğrudan çalıştırılır.
func (a *discordApp) start(proxyURL string) error {
	var cmd *exec.Cmd
	if upd := filepath.Join(a.dir, "Update.exe"); fileExists(upd) {
		cmd = exec.Command(upd, "--processStart", a.exe, "--process-start-args=--proxy-server="+proxyURL)
	} else if exe := a.latestExe(); exe != "" {
		cmd = exec.Command(exe, "--proxy-server="+proxyURL)
	} else {
		return fmt.Errorf("%s içinde Update.exe ya da %s bulunamadı", a.dir, a.exe)
	}
	cmd.Env = append(os.Environ(), proxyEnv(proxyURL)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func (a *discordApp) latestExe() string {
	dirs, _ := filepath.Glob(filepath.Join(a.dir, "app-*"))
	sort.Slice(dirs, func(i, j int) bool { return versionLess(dirs[i], dirs[j]) })
	for i := len(dirs) - 1; i >= 0; i-- {
		if exe := filepath.Join(dirs[i], a.exe); fileExists(exe) {
			return exe
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

func manualHint(proxyURL string) string {
	return `%LOCALAPPDATA%\Discord\Update.exe --processStart Discord.exe --process-start-args=--proxy-server=` + proxyURL
}
