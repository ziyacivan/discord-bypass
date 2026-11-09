//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Windows dışında tepsi uygulamasının sistem entegrasyonları yoktur; macOS'ta
// bunları menü çubuğu uygulaması üstlenir.

var errUnsupported = errors.New("bu platformda desteklenmiyor")

func isGUI() bool              { return false }
func autostartSupported() bool { return false }
func autostartEnabled() bool   { return false }
func setAutostart(bool) error  { return errUnsupported }
func healAutostart()           {}
func browserSupported() bool   { return false }
func readPAC() (string, bool)  { return "", false }
func writePAC(string) error    { return errUnsupported }
func clearPAC() error          { return errUnsupported }
func showError(string)         {}

func openPanelWindow(url string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}

// listClients, lsof ile 127.0.0.1:port'a bağlı süreçleri bulur.
func listClients(port int) []clientApp {
	out, err := exec.Command("lsof", "+c", "0", "-nP", "-iTCP@127.0.0.1:"+strconv.Itoa(port),
		"-sTCP:ESTABLISHED", "-Fpcn").Output()
	if err != nil {
		return nil
	}
	self := os.Getpid()
	pid, name := 0, ""
	counts := map[string]int{}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'c':
			name = line[1:]
		case 'n':
			if pid != self {
				counts[name]++
			}
		}
	}
	return sortClients(counts)
}
