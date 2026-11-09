package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type discordApp struct {
	name   string // "Discord", "Discord PTB", "Discord Canary"
	bundle string // /Applications/Discord.app
}

func findDiscord(hint string) (*discordApp, error) {
	var cands []string
	if hint != "" {
		cands = []string{strings.TrimSuffix(hint, "/")}
	} else {
		home, _ := os.UserHomeDir()
		for _, n := range []string{"Discord", "Discord PTB", "Discord Canary"} {
			cands = append(cands, "/Applications/"+n+".app", filepath.Join(home, "Applications", n+".app"))
		}
	}
	for _, p := range cands {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() && strings.HasSuffix(p, ".app") {
			return &discordApp{name: strings.TrimSuffix(filepath.Base(p), ".app"), bundle: p}, nil
		}
	}
	return nil, errors.New("Discord bulunamadı (/Applications/Discord.app); -app ile yolunu verin")
}

// Ana süreç: <bundle>/Contents/MacOS/<ad>. Helper süreçleri Frameworks altındadır.
func (a *discordApp) mainPattern() string {
	return regexp.QuoteMeta(a.bundle + "/Contents/MacOS/")
}

func (a *discordApp) running() bool {
	return exec.Command("pgrep", "-f", a.mainPattern()).Run() == nil
}

func (a *discordApp) usingProxy(proxyURL string) bool {
	pat := a.mainPattern() + ".*" + regexp.QuoteMeta("--proxy-server="+proxyURL)
	return exec.Command("pgrep", "-f", pat).Run() == nil
}

// quit, Discord'u SIGTERM ile kapatır (Electron bunu normal çıkış olarak
// işler). AppleScript kullanılmaz; böylece "Terminal, Discord'u kontrol etmek
// istiyor" izin penceresi çıkmaz.
func (a *discordApp) quit() error {
	exec.Command("pkill", "-TERM", "-f", a.mainPattern()).Run()
	if waitUntil(10*time.Second, func() bool { return !a.running() }) {
		time.Sleep(500 * time.Millisecond) // LaunchServices'in süreci bırakması için
		return nil
	}
	exec.Command("pkill", "-KILL", "-f", regexp.QuoteMeta(a.bundle+"/Contents/")).Run()
	if waitUntil(5*time.Second, func() bool { return !a.running() }) {
		time.Sleep(500 * time.Millisecond)
		return nil
	}
	return errors.New("Discord kapatılamadı; elle kapatıp (Cmd+Q) tekrar deneyin")
}

// start, Discord'u LaunchServices (open) ile başlatır. Doğrudan çalıştırmak
// yerine open kullanılır; böylece mikrofon gibi izinler Terminal'e değil
// Discord'a ait kalır.
func (a *discordApp) start(proxyURL string) error {
	args := []string{"-a", a.bundle}
	for _, e := range proxyEnv(proxyURL) {
		args = append(args, "--env", e)
	}
	args = append(args, "--args", "--proxy-server="+proxyURL)
	if out, err := exec.Command("open", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func manualHint(proxyURL string) string {
	return `open -a Discord --args --proxy-server=` + proxyURL
}
