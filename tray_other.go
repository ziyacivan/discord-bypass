//go:build !windows

package main

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// runTray, Windows dışında simge göstermez; panel tarayıcıdan kullanılır.
// macOS için asıl arayüz menü çubuğu uygulamasıdır (macos/ klasörü).
func runTray(c *controller) {
	done := make(chan struct{})
	var once sync.Once
	c.mu.Lock()
	c.quit = func() { once.Do(func() { close(done) }) }
	c.mu.Unlock()

	log.Printf("tepsi simgesi yalnızca Windows'ta var; panel: %s", c.panelURL())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-done:
	}
}
