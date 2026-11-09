# discord-bypass

Discord'a erişim için hafif, **sistem ayarlarına hiç dokunmayan** yerel proxy.
macOS ve Windows'ta çalışır.

```
./discord-bypass
```

Program yerel bir proxy açar, bulunduğunuz ağda çalışan yöntemi kendisi seçer
ve Discord'u bu proxy ile yeniden başlatır. Kapattığınızda geri alınacak bir şey
kalmaz.

macOS'ta **menü çubuğu**, Windows'ta **sistem tepsisi** uygulaması da var: tek
tıkla aç/kapa, proxy'den geçen her alan adının canlı listesi, bilgisayar
açılınca otomatik başlatma ve isteğe bağlı tarayıcı desteği.

## Neden gecit değil?

[gecit](https://github.com/boratanrikulu/gecit) bütün sistemi etkiler. Sistem DNS'ini
`127.0.0.1`'e çevirir, macOS'ta `mDNSResponder`'ı öldürür ve TUN arayüzüyle tüm
IPv4 trafiğini kendi üzerinden geçirir. Süreç düzgün kapanmazsa (çökme, uyku,
yeniden başlatma) DNS `127.0.0.1`'de kalır ve internet tamamen gider.

|                         | gecit (macOS/Windows)                  | discord-bypass                     |
|-------------------------|----------------------------------------|------------------------------------|
| Etkilediği trafik       | Bilgisayardaki tüm trafik (TUN)        | Yalnızca Discord                   |
| Sistem DNS'i            | `127.0.0.1` yapılır                    | Dokunulmaz                         |
| Route / ağ arayüzü      | TUN + varsayılan route'lar             | Dokunulmaz                         |
| Yetki                   | root / Yönetici                        | Normal kullanıcı                   |
| Windows ek gereksinim   | Npcap                                  | Yok                                |
| Çökerse                 | DNS/route bozuk kalabilir              | Yalnızca Discord bağlantısı kopar  |
| VPN/Tailscale ile       | İki TUN çakışabilir                    | Etkilenmez                         |

## Nasıl çalışır?

```
Discord ──CONNECT discord.com:443──▶ discord-bypass (127.0.0.1:8899)
                                        │ 1. discord.com'u DoH ile çözer (1.1.1.1 / 8.8.8.8)
                                        │    → ISS DNS'inin zehirli cevabı kullanılmaz
                                        │ 2. TLS ClientHello'yu SNI'nin ortasından böler
                                        │    → DPI "discord.com" adını göremez
                                        ▼
                                   Discord sunucusu (parçaları birleştirir)
```

Türkiye'de Discord iki katmanda engelleniyor:

1. **DNS zehirleme:** ISS DNS'i `discord.com` için `195.175.254.2` (BTK uyarı
   sayfası) döndürüyor. Program Discord alan adlarını DNS-over-HTTPS ile çözer.
   Sistem DNS'i değişmez; bu çözümleme yalnızca programın kendi bağlantılarında
   kullanılır.
2. **SNI tabanlı DPI:** TLS el sıkışmasındaki host adı görülünce bağlantı
   düşürülüyor. Program ClientHello'yu parçalayarak gönderir.

Discord bir Electron (Chromium) uygulaması olduğu için `--proxy-server`
argümanını destekler. Bu argüman yalnızca o Discord sürecine verilir, sistemde
kalıcı bir ayar oluşturmaz. Sesli sohbet (UDP) proxy'den geçmez, doğrudan gider.

## Kurulum

[Go](https://go.dev/dl/) 1.26+ gerekir. Daha eski bir Go sürümü varsa gerekli
sürümü kendisi indirir.

```bash
make            # bin/ altına macOS ve Windows sürümlerini derler
# ya da yalnızca bulunduğunuz sistem için:
go build -o discord-bypass .
```

| Dosya                                     | Ne                                        |
|-------------------------------------------|-------------------------------------------|
| `bin/Discord Bypass.app` (`make app`)     | macOS menü çubuğu uygulaması              |
| `bin/discord-bypass-macos-arm64`          | macOS terminal sürümü, Apple Silicon      |
| `bin/discord-bypass-macos-amd64`          | macOS terminal sürümü, Intel              |
| `bin/windows-x64/DiscordBypass.exe`       | Windows tepsi uygulaması (çoğu bilgisayar)|
| `bin/windows-x64/discord-bypass.exe`      | Windows terminal sürümü                   |
| `bin/windows-arm64/…`                     | Aynıları, Windows on ARM için             |

Windows sürümü macOS'ta derlenebilir (cgo yok). `make app` uygulama paketini
silip yeniden oluşturur; `bin/` içinden çalışan uygulamadan önce çıkın.

## Kullanım

### macOS menü çubuğu uygulaması (önerilen)

```bash
make app            # bin/Discord Bypass.app (Xcode Command Line Tools gerekir)
make install-app    # /Applications'a kopyalar
```

Uygulama menü çubuğunda bir kalkan simgesi olarak durur, Dock'ta görünmez.
Simgeye tıklayınca açılan panelde:

- **Aç/kapa anahtarı:** Bypass'ı başlatır ya da durdurur. Şifre istemez.
  Kapatınca Discord açık kalır ama bağlantısı kopar; açınca kendiliğinden
  yeniden bağlanır.
- **Canlı bağlantı listesi:** Proxy'den geçen her alan adı. `BYPASS` olanlar
  DoH ve ClientHello parçalamadan geçer, `DİREKT` olanlar olduğu gibi iletilir.
  Filtreler: Tümü, Discord, Diğer, Sorun.
- **Sorunlar:** Bağlanılamayan ya da ClientHello'ya hiç yanıt gelmeyen (DPI
  engeli olabilir) hostlar kırmızı/turuncu işaretlenir.
- **Proxy'yi kullanan:** Proxy'ye o an hangi uygulamaların bağlı olduğu (ör.
  `Discord Helper`). Discord dışında bir şey görürseniz hemen fark edersiniz.
- **Kayıt** sekmesi, **Discord'u yeniden başlat**, **yöntem seçimi**,
  **oturum açılınca başlat**.

Uygulama yalnızca bir arayüzdür; asıl işi paketin içindeki aynı Go çekirdeği
yapar. Uygulama çökerse çekirdek de kendiliğinden kapanır.

Terminal sürümü 8899 portunu kullanıyorsa uygulama başlayamaz. Önce terminaldeki
`discord-bypass`'ı kapatın.

### macOS (terminal)

```bash
./discord-bypass
```

- `sudo` **gerekmez**.
- Discord açıksa kapatılır ve proxy ile yeniden açılır.
- Terminal penceresi açık kalmalıdır. `Ctrl+C` ile durdurulur.

### Windows tepsi uygulaması (önerilen)

1. `DiscordBypass.exe`'yi kalıcı bir klasöre koyun, ör.
   `%LOCALAPPDATA%\Programs\DiscordBypass\`. "Windows açılınca başlat" bu yolu
   kaydeder; dosyayı sonradan taşırsanız ilk açılışta kendini günceller.
2. Çift tıklayın. Konsol penceresi açılmaz; saatin yanındaki tepside kalkan
   simgesi belirir ve panel açılır.

- **Sol tık:** Paneli açar. Panel, Edge'in uygulama modunda adres çubuğu
  olmayan küçük bir pencere olarak açılır; macOS panelindeki her şey burada da
  var.
- **Sağ tık menüsü:** Durum, bypass aç/kapa, paneli aç, Discord'u yeniden
  başlat, yöntem, **Windows açılınca başlat**, tarayıcılarda da kullan,
  başlatınca Discord'u aç, çık.
- **Simge rengi:** Yeşil açık, turuncu başlatılıyor, gri kapalı, kırmızı hata.
- **Windows açılınca başlat:** `HKCU\…\CurrentVersion\Run` kaydı. Oturum
  açıldığında panel açılmadan tepside sessizce başlar. Görev Yöneticisi →
  Başlangıç uygulamaları listesinde de görünür.
- **Tarayıcılarda da kullan:** Kullanıcıya özel "otomatik proxy betiği" ayarına
  (`AutoConfigURL`) PAC adresini yazar. Yönetici yetkisi istemez, önceki ayarı
  saklar ve kapatınca geri yükler.
- Yönetici yetkisi ve Npcap **gerekmez**. Tüm ayarlar yalnızca sizin kullanıcı
  hesabınızı etkiler.
- Ayarlar ve kayıt dosyası: `%APPDATA%\discord-bypass\`.
- Uygulama zaten açıkken tekrar çalıştırırsanız ikinci kopya açılmaz, mevcut
  panel gelir.
- SmartScreen imzasız dosya uyarısı verirse: "Ek bilgi" → "Yine de çalıştır".
  Otomatik başlatma ve proxy ayarı değiştiren imzasız programlar Defender'da
  yanlış alarm verebilir; kaynak koddan kendiniz derleyebilirsiniz.

### Windows (terminal)

```powershell
.\discord-bypass.exe
```

- Konsol penceresi açık kalmalıdır. Pencereyi kapatmak programı durdurur.

### Seçenekler

```
-mode auto        yöntem: auto, tlsrec+split, tlsrec, split, disorder, none
-port 8899        yerel proxy portu
-launch=false     Discord'u başlatma, yalnızca proxy'yi çalıştır
-app <yol>        Discord'un yolu (PTB/Canary ya da farklı konum için)
-doh cloudflare,google   DoH sunucuları (quad9 ya da https:// adresi de olur)
-domains a.com,b.com     Discord listesine alan adı ekle
-all              proxy'den geçen her alan adına uygula
-iface en0        giden bağlantıları bu arayüze bağla (VPN/TUN çakışmasında)
-v                her bağlantıyı logla
```

### Teşhis: `probe`

```bash
./discord-bypass probe
```

DNS'in zehirli olup olmadığını ve her yöntemin çalışıp çalışmadığını gösterir.
Türkiye'deki bir ev bağlantısında (Kasım 2026) alınan çıktı (ilk satırın süresi
DoH sorgusunu da içerir):

```
2) TLS: DoH ile bulunan IP'ye her yöntemle bağlantı
   discord.com
     ✓ tlsrec+split  1189 ms
     ✓ tlsrec        279 ms
     ✗ split         bağlantı sıfırlandı (RST, muhtemelen DPI)
     ✓ disorder      406 ms
     ✗ none          zaman aşımı (paketler düşürülüyor olabilir)
```

Bu ağdaki DPI, TCP segmentlerini birleştiriyor ama TLS kayıtlarını
birleştirmiyor. Bu yüzden yalnızca `split` yetmiyor, `tlsrec` çalışıyor.

## Yöntemler

| Yöntem         | Ne yapar                                                                   | Gecikme  |
|----------------|----------------------------------------------------------------------------|----------|
| `tlsrec+split` | ClientHello'yu SNI ortasından iki TLS kaydına böler, ayrı TCP segmentlerinde gönderir | ~5 ms |
| `tlsrec`       | Yalnızca iki TLS kaydına böler                                             | 0        |
| `split`        | Yalnızca TCP segmentlerine böler                                           | ~5 ms    |
| `disorder`     | İlk parçayı TTL=1 ile gönderir; DPI parçaları ters sırada görür            | ~100+ ms |
| `none`         | Yalnızca DoH, parçalama yok                                                |          |

`auto` modu bu sırayla dener ve `discord.com`'a ulaşan ilk yöntemi seçer. Hiçbiri
root yetkisi gerektirmez.

## Tarayıcıda Discord

**Menü çubuğu uygulamasıyla:** Paneldeki "Tarayıcılar → Kur" düğmesi, macOS
sistem proxy ayarına bir PAC adresi (`http://127.0.0.1:8898/proxy.pac`) ekler.
Safari, Chrome, Firefox ve sistem proxy'sine uyan diğer uygulamalar bu ayarı
kullanır.

- PAC yalnızca Discord alan adlarını `PROXY 127.0.0.1:8899; DIRECT` yapar.
  Diğer her şey doğrudan gider ve listede görünmez.
- Kurmak ve kaldırmak için birer kez yönetici şifresi sorulur. Aç/kapa için
  şifre gerekmez.
- Bypass kapalıyken ya da uygulama kapalı veya çökmüşken tarayıcı proxy'ye
  ulaşamaz ve doğrudan bağlanır. gecit'teki gibi internetin kesilmesi söz
  konusu değildir.
- Kurulumdan önceki proxy ayarları saklanır. "Kaldır" düğmesi onları geri
  yükler.
- Uygulamayı silmeden önce "Kaldır"a basın. Unuttuysanız:
  `networksetup -setautoproxystate Wi-Fi off` (her servis için).
- Chrome, proxy'yi kapalı bulunca onu birkaç dakika "kötü" sayabilir. Bypass'ı
  yeniden açtıktan sonra tarayıcıda Discord hemen gelmezse sekmeyi yenileyin
  ya da birkaç dakika bekleyin.

**Windows tepsi uygulamasıyla:** Sağ tık menüsünde ya da panelde
"Tarayıcılarda da kullan". Şifre istemez; adres `http://127.0.0.1:8897/proxy.pac`
olur. Uygulamayı silmeden önce kapatın. Unuttuysanız: Ayarlar → Ağ ve İnternet →
Ara sunucu → "Kurulum betiği kullan" seçeneğini kapatın.

**Elle (terminal sürümü):** Proxy aynı PAC dosyasını
`http://127.0.0.1:8899/proxy.pac` adresinde sunar. Firefox'ta: Ayarlar → Ağ
Ayarları → "Otomatik proxy yapılandırma URL'si".

## Sorun giderme

**gecit'ten geçiyorsanız DNS'i düzeltin.** gecit düzgün kapanmadığında yedek
dosyasına `127.0.0.1`'i "orijinal DNS" olarak kaydedebiliyor. Bu durumda gecit
her kapandığında DNS'i tekrar `127.0.0.1`'e "geri yükler" ve internet kesilir.

macOS (önce gecit'i `Ctrl+C` ile durdurun; `gecit cleanup` kullanmayın, yedek
dosyası bozuksa DNS'i yine `127.0.0.1` yapar):

```bash
networksetup -listallnetworkservices     # servis adlarını görün
sudo networksetup -setdnsservers Ethernet empty   # 127.0.0.1 görünen her servis için
sudo networksetup -setdnsservers Wi-Fi empty
sudo rm -f /tmp/gecit-dns-backup
sudo killall -HUP mDNSResponder
```

Windows (Yönetici PowerShell):

```powershell
gecit cleanup
Get-DnsClientServerAddress -AddressFamily IPv4   # 127.0.0.1 olan arayüzü bulun
Set-DnsClientServerAddress -InterfaceAlias "Ethernet" -ResetServerAddresses
Remove-Item "$env:ProgramData\gecit-dns-backup" -ErrorAction SilentlyContinue
ipconfig /flushdns
```

**Discord güncellendikten sonra bağlanamıyor.** Güncelleme sonrası Discord
kendini proxy argümanı olmadan yeniden başlatabilir. `discord-bypass`'ı yeniden
çalıştırın.

**"port kullanımda" hatası.** Program zaten açık olabilir. Değilse `-port 8900`
gibi başka bir port verin.

**VPN, Tailscale exit node ya da başka bir TUN yazılımı açık.** Discord trafiği
o tünele gidebilir. `-iface en0` (macOS) ya da `-iface "Wi-Fi"` (Windows) ile
fiziksel arayüzü zorlayın.

## Sınırlamalar

- Yalnızca bu programla başlatılan Discord etkilenir. Discord'u Dock'tan ya da
  Başlat menüsünden normal açarsanız proxy kullanılmaz. (macOS'ta tarayıcı
  desteği kuruluysa Discord da sistem PAC ayarına uyar.)
- Sesli sohbet UDP ile doğrudan gider. Bir gün UDP de engellenirse bu araç
  (gecit de) yardımcı olmaz.
- IP tabanlı engellemeyi aşmaz. Bu bir VPN değildir; IP adresinizi gizlemez.

> Bu proje eğitim ve araştırma amaçlıdır. Kullanım sorumluluğu kullanıcıya aittir.
