package main

import (
	_ "embed"
	"sync"

	"fyne.io/systray"
)

var (
	//go:embed assets/tray-on.ico
	icoOn []byte
	//go:embed assets/tray-off.ico
	icoOff []byte
	//go:embed assets/tray-starting.ico
	icoStarting []byte
	//go:embed assets/tray-error.ico
	icoError []byte
)

// runTray, tepsi simgesini gösterir ve kullanıcı "Çık" diyene kadar bekler.
// Sol tık paneli açar, sağ tık menüyü gösterir.
func runTray(c *controller) {
	systray.Run(func() { setupTray(c) }, func() {})
}

func setupTray(c *controller) {
	open := func() { openPanelWindow(c.panelURL()) }

	systray.SetIcon(icoOff)
	systray.SetTitle("Discord Bypass")
	systray.SetTooltip("Discord Bypass")
	systray.SetOnTapped(open)

	mStatus := systray.AddMenuItem("Discord Bypass", "")
	mStatus.Disable()
	systray.AddSeparator()
	mToggle := systray.AddMenuItemCheckbox("Bypass açık", "Bypass'ı aç ya da kapat", false)
	mPanel := systray.AddMenuItem("Paneli aç", "Canlı bağlantı listesi ve ayarlar")
	mRelaunch := systray.AddMenuItem("Discord'u proxy ile yeniden başlat", "")
	systray.AddSeparator()
	mMode := systray.AddMenuItem("Yöntem", "")
	modeItems := map[string]*systray.MenuItem{}
	for _, name := range append([]string{"auto"}, modeList()...) {
		modeItems[name] = mMode.AddSubMenuItemCheckbox(name, "", false)
	}
	mAutostart := systray.AddMenuItemCheckbox("Windows açılınca başlat", "Oturum açıldığında tepside sessizce başlar", false)
	mBrowser := systray.AddMenuItemCheckbox("Tarayıcılarda da kullan", "Sistem proxy ayarına yalnızca Discord için PAC ekler", false)
	mLaunch := systray.AddMenuItemCheckbox("Başlatınca Discord'u proxy ile aç", "", false)
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Çık", "Bypass'ı kapatır ve uygulamadan çıkar")

	c.mu.Lock()
	c.quit = systray.Quit
	c.mu.Unlock()

	var mu sync.Mutex
	refresh := func() {
		mu.Lock()
		defer mu.Unlock()
		c.mu.Lock()
		state, modeName, errMsg, s := c.state, c.modeName, c.errMsg, c.settings
		c.mu.Unlock()

		icon, text := icoOff, "Kapalı"
		switch state {
		case "running":
			icon, text = icoOn, "Açık · "+modeName
		case "starting":
			icon, text = icoStarting, "Başlatılıyor…"
		case "failed":
			icon, text = icoError, "Hata: "+truncate(errMsg, 60)
		}
		systray.SetIcon(icon)
		systray.SetTooltip(truncate("Discord Bypass · "+text, 120))
		mStatus.SetTitle(text)
		setChecked(mToggle, state == "running" || state == "starting")
		if state == "running" {
			mRelaunch.Enable()
		} else {
			mRelaunch.Disable()
		}
		for name, it := range modeItems {
			setChecked(it, name == s.Mode)
		}
		setChecked(mAutostart, autostartEnabled())
		setChecked(mBrowser, c.browserInstalled())
		setChecked(mLaunch, s.LaunchDiscord)
	}
	c.setOnChange(refresh)
	refresh()

	report := func(err error) {
		if err != nil {
			go showError(err.Error())
		}
	}
	go func() {
		for {
			select {
			case <-mToggle.ClickedCh:
				c.toggle()
			case <-mPanel.ClickedCh:
				open()
			case <-mRelaunch.ClickedCh:
				go c.launchDiscord(true)
			case <-mAutostart.ClickedCh:
				report(c.setAutostart(!autostartEnabled()))
			case <-mBrowser.ClickedCh:
				report(c.setBrowser(!c.browserInstalled()))
			case <-mLaunch.ClickedCh:
				c.setLaunchDiscord(!mLaunch.Checked())
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()
	for name, it := range modeItems {
		go func(name string, it *systray.MenuItem) {
			for range it.ClickedCh {
				report(c.setMode(name))
			}
		}(name, it)
	}
}

func setChecked(it *systray.MenuItem, on bool) {
	if on {
		it.Check()
	} else {
		it.Uncheck()
	}
}
