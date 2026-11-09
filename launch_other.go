//go:build !darwin && !windows

package main

import "errors"

// Diğer platformlarda proxy çalışır ama Discord otomatik başlatılmaz.
type discordApp struct{ name string }

func findDiscord(string) (*discordApp, error) {
	return nil, errors.New("otomatik başlatma yalnızca macOS ve Windows'ta destekleniyor")
}

func (a *discordApp) running() bool          { return false }
func (a *discordApp) usingProxy(string) bool { return false }
func (a *discordApp) quit() error            { return nil }
func (a *discordApp) start(string) error     { return nil }
func manualHint(proxyURL string) string      { return "discord --proxy-server=" + proxyURL }
