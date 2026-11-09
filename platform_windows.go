package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Hepsi HKEY_CURRENT_USER altında: yönetici yetkisi gerekmez ve yalnızca bu
// kullanıcıyı etkiler.
const (
	runKeyPath  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue    = "Discord Bypass"
	inetKeyPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
)

func isGUI() bool { return defaultCmd == "tray" }

// MARK: Windows açılınca başlat

func autostartSupported() bool { return true }

func autostartCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return `"` + exe + `" -autostart`, nil
}

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func setAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	cmd, err := autostartCommand()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValue, cmd)
}

// healAutostart, uygulama başka bir klasöre taşındıysa kayıttaki yolu günceller.
func healAutostart() {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	cur, _, err := k.GetStringValue(runValue)
	if err != nil {
		return
	}
	if want, err := autostartCommand(); err == nil && !strings.EqualFold(cur, want) {
		k.SetStringValue(runValue, want)
	}
}

// MARK: Tarayıcı desteği (kullanıcıya özel otomatik proxy betiği)

func browserSupported() bool { return true }

func readPAC() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("AutoConfigURL")
	if err != nil {
		return "", true
	}
	return v, true
}

func writePAC(url string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, inetKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue("AutoConfigURL", url); err != nil {
		return err
	}
	refreshInternetSettings()
	return nil
}

func clearPAC() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue("AutoConfigURL"); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	refreshInternetSettings()
	return nil
}

// refreshInternetSettings, açık uygulamalara (Edge, Chrome) ayarın değiştiğini bildirir.
func refreshInternetSettings() {
	proc := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	if proc.Find() != nil {
		return
	}
	const settingsChanged, refresh = 39, 37
	proc.Call(0, settingsChanged, 0, 0)
	proc.Call(0, refresh, 0, 0)
}

// MARK: Panel penceresi

// openPanelWindow, paneli Edge'in uygulama modunda (adres çubuğu olmayan küçük
// pencere) açar. Edge yoksa varsayılan tarayıcı kullanılır.
func openPanelWindow(url string) error {
	if edge := findEdge(); edge != "" {
		cmd := exec.Command(edge, "--app="+url, "--window-size=520,780")
		if err := cmd.Start(); err == nil {
			go cmd.Wait()
			return nil
		}
	}
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd.Start()
}

func findEdge() string {
	const appPath = `Software\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		if k, err := registry.OpenKey(root, appPath, registry.QUERY_VALUE); err == nil {
			v, _, err := k.GetStringValue("")
			k.Close()
			if err == nil && fileExists(v) {
				return v
			}
		}
	}
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if base := os.Getenv(env); base != "" {
			if p := filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`); fileExists(p) {
				return p
			}
		}
	}
	return ""
}

// MARK: Proxy'yi kullanan uygulamalar

// listClients, 127.0.0.1:port'a bağlı süreçleri netstat ile bulur.
// Durum sütunu Türkçe Windows'ta yerelleştirildiği için ona bakılmaz.
func listClients(port int) []clientApp {
	cmd := exec.Command("netstat", "-ano", "-p", "TCP")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	target := "127.0.0.1:" + strconv.Itoa(port)
	self := uint32(os.Getpid())
	perPID := map[uint32]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || !strings.EqualFold(f[0], "TCP") || f[2] != target {
			continue
		}
		pid, err := strconv.ParseUint(f[len(f)-1], 10, 32)
		if err != nil || pid == 0 || uint32(pid) == self {
			continue
		}
		perPID[uint32(pid)]++
	}
	names := processNames()
	counts := map[string]int{}
	for pid, n := range perPID {
		name := names[pid]
		if name == "" {
			name = "PID " + strconv.Itoa(int(pid))
		}
		counts[name] += n
	}
	return sortClients(counts)
}

// processNames, çalışan süreçlerin PID → exe adı eşlemesini döndürür.
func processNames() map[uint32]string {
	m := map[uint32]string{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return m
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err := windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		m[pe.ProcessID] = windows.UTF16ToString(pe.ExeFile[:])
	}
	return m
}

// showError, tepsi uygulamasında hataları bir uyarı penceresiyle gösterir.
func showError(msg string) {
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
	title, _ := windows.UTF16PtrFromString("Discord Bypass")
	text, _ := windows.UTF16PtrFromString(msg)
	const mbIconError = 0x10
	proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
}
