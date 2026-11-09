## Hangi dosyayı indirmeliyim?

| Sistem | Dosya |
|---|---|
| **Windows** (çoğu bilgisayar) | **`DiscordBypass.exe`**: tepsi uygulaması |
| Windows on ARM (Snapdragon vb.) | `DiscordBypass-arm64.exe` |
| **macOS 13+** (Apple Silicon ve Intel) | **`DiscordBypass-macOS.zip`**: menü çubuğu uygulaması |
| Terminal sürümü (Windows) | `discord-bypass.exe`, `discord-bypass-arm64.exe` |

### Windows

1. `DiscordBypass.exe`'yi kalıcı bir klasöre koyun, ör. `%LOCALAPPDATA%\Programs\DiscordBypass\`.
   "Windows açılınca başlat" bu yolu kaydeder.
2. Çift tıklayın. SmartScreen "Windows bilgisayarınızı korudu" derse **Ek bilgi → Yine de çalıştır**.
3. Saatin yanındaki kalkan simgesi: sol tık paneli açar, sağ tık menüyü.

Yönetici yetkisi gerekmez; sistem DNS'ine ve ağ ayarlarına dokunulmaz.

### macOS

1. Zip'i açın, `Discord Bypass.app`'i **Uygulamalar** klasörüne taşıyın.
2. Uygulama imzasız olduğu için ilk açılışta macOS engeller:
   **Sistem Ayarları → Gizlilik ve Güvenlik → Yine de Aç**. Ya da Terminal'de:
   ```bash
   xattr -dr com.apple.quarantine "/Applications/Discord Bypass.app"
   ```
3. Menü çubuğundaki kalkan simgesine tıklayın.

Dosyaların SHA-256 özetleri `SHA256SUMS.txt` içinde.
