import AppKit
import Foundation
import Network

// Tarayıcı desteği: macOS'in sistem proxy ayarına bir PAC adresi yazılır.
// PAC yalnızca Discord alan adlarını "PROXY 127.0.0.1:8899; DIRECT" yapar,
// gerisi DIRECT kalır. Bypass kapalıyken ya da uygulama çökmüşken tarayıcılar
// proxy'ye ulaşamaz ve otomatik olarak doğrudan bağlanır; internet kesilmez.
//
// Kurulum ve kaldırma için bir kez yönetici şifresi sorulur. Orijinal ayarlar
// kalıcı olarak saklanır ve kendi PAC adresimiz asla "orijinal" diye
// kaydedilmez.

@MainActor
final class BrowserSupport: ObservableObject {
    static let shared = BrowserSupport()

    @Published private(set) var installed = false
    @Published private(set) var busy = false
    @Published private(set) var message: String?
    @Published private(set) var serverRunning = false

    private let server = PACServer()
    private let backupKey = "pacBackup"

    var pacURL: String { "http://127.0.0.1:\(Settings.pacPort)/proxy.pac" }

    // MARK: - PAC sunucusu

    /// PAC dosyasını uygulama açık olduğu sürece sunar (bypass kapalıyken de).
    func startServer() {
        let script = Self.loadPACScript()
        do {
            try server.start(port: UInt16(Settings.pacPort), script: script)
            serverRunning = true
        } catch {
            serverRunning = false
            message = "PAC sunucusu başlatılamadı (port \(Settings.pacPort)): \(error.localizedDescription)"
        }
        refresh()
    }

    /// PAC içeriği Go çekirdeğinden alınır; alan adı listesi tek yerde durur.
    static func loadPACScript() -> String {
        if let exe = Core.coreExecutable() {
            let p = Process()
            p.executableURL = exe
            p.arguments = ["pac", "-port", String(Settings.proxyPort)]
            let pipe = Pipe()
            p.standardOutput = pipe
            p.standardError = FileHandle.nullDevice
            if (try? p.run()) != nil {
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                p.waitUntilExit()
                if p.terminationStatus == 0, let s = String(data: data, encoding: .utf8), s.contains("FindProxyForURL") {
                    return s
                }
            }
        }
        // Çekirdek yoksa en temel liste.
        return """
        function FindProxyForURL(url, host) {
          var d = ["discord.com", "discord.gg", "discordapp.com", "discordapp.net", "discord.media", "discordcdn.com"];
          for (var i = 0; i < d.length; i++) {
            if (host === d[i] || dnsDomainIs(host, "." + d[i])) return "PROXY 127.0.0.1:\(Settings.proxyPort); DIRECT";
          }
          return "DIRECT";
        }
        """
    }

    // MARK: - Sistem ayarı

    struct ServiceProxy {
        let service: String
        let url: String?
        let enabled: Bool
    }

    func refresh() {
        let ours = pacURL
        DispatchQueue.global(qos: .utility).async { [weak self] in
            let list = Self.readServices()
            let on = list.contains { $0.enabled && $0.url == ours }
            DispatchQueue.main.async { self?.installed = on }
        }
    }

    func toggle() {
        installed ? uninstall() : install()
    }

    func install() {
        let ours = pacURL
        run(action: "kurulamadı") {
            let services = Self.readServices()
            guard !services.isEmpty else { throw SupportError.message("Ağ servisi bulunamadı.") }

            // Yedek: zaten bizim adresimizi gösteren servisler yedeklenmez.
            var backup = Settings.defaults.dictionary(forKey: self.backupKey) as? [String: [String: Any]] ?? [:]
            for s in services where s.url != ours && backup[s.service] == nil {
                backup[s.service] = ["url": s.url ?? "", "enabled": s.enabled]
            }

            let cmds = services.flatMap { s in [
                "/usr/sbin/networksetup -setautoproxyurl \(shQuote(s.service)) \(shQuote(ours))",
                "/usr/sbin/networksetup -setautoproxystate \(shQuote(s.service)) on",
            ] }
            try Self.runAsAdmin(cmds.joined(separator: " && "))
            Settings.defaults.set(backup, forKey: self.backupKey)
        }
    }

    func uninstall() {
        let ours = pacURL
        run(action: "kaldırılamadı") {
            let services = Self.readServices()
            let backup = Settings.defaults.dictionary(forKey: self.backupKey) as? [String: [String: Any]] ?? [:]
            var cmds: [String] = []
            for s in services where s.url == ours {
                let svc = shQuote(s.service)
                if let orig = backup[s.service], let url = orig["url"] as? String, !url.isEmpty, url != ours {
                    cmds.append("/usr/sbin/networksetup -setautoproxyurl \(svc) \(shQuote(url))")
                    let on = (orig["enabled"] as? Bool) ?? false
                    cmds.append("/usr/sbin/networksetup -setautoproxystate \(svc) \(on ? "on" : "off")")
                } else {
                    cmds.append("/usr/sbin/networksetup -setautoproxystate \(svc) off")
                }
            }
            if !cmds.isEmpty {
                try Self.runAsAdmin(cmds.joined(separator: " && "))
            }
            Settings.defaults.removeObject(forKey: self.backupKey)
        }
    }

    private func run(action: String, _ body: @escaping () throws -> Void) {
        guard !busy else { return }
        busy = true
        message = nil
        NSApp.activate(ignoringOtherApps: true)
        // Şifre penceresi ana iş parçacığında gösterilir.
        DispatchQueue.main.async {
            do {
                try body()
            } catch SupportError.cancelled {
                // Kullanıcı vazgeçti; mesaj gösterme.
            } catch {
                self.message = "Tarayıcı desteği \(action): \(error.localizedDescription)"
            }
            self.busy = false
            self.refresh()
        }
    }

    enum SupportError: LocalizedError {
        case cancelled
        case message(String)
        var errorDescription: String? {
            switch self {
            case .cancelled: return "iptal edildi"
            case .message(let m): return m
            }
        }
    }

    nonisolated static func readServices() -> [ServiceProxy] {
        let list = runTool(["-listallnetworkservices"])
            .split(separator: "\n")
            .dropFirst() // "An asterisk (*) denotes ..." satırı
            .map { $0.hasPrefix("*") ? String($0.dropFirst()) : String($0) }
            .filter { !$0.isEmpty }
        return list.map { svc in
            var url: String?
            var enabled = false
            for line in runTool(["-getautoproxyurl", svc]).split(separator: "\n") {
                if line.hasPrefix("URL: ") {
                    let v = String(line.dropFirst(5))
                    url = v == "(null)" ? nil : v
                } else if line.hasPrefix("Enabled: ") {
                    enabled = line.hasSuffix("Yes")
                }
            }
            return ServiceProxy(service: svc, url: url, enabled: enabled)
        }
    }

    nonisolated static func runTool(_ args: [String]) -> String {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/sbin/networksetup")
        p.arguments = args
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = FileHandle.nullDevice
        guard (try? p.run()) != nil else { return "" }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()
        return String(decoding: data, as: UTF8.self)
    }

    /// Komutu macOS'in standart yönetici şifresi penceresiyle çalıştırır.
    static func runAsAdmin(_ command: String) throws {
        let escaped = command
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "\"", with: "\\\"")
        let prompt = "Discord Bypass, tarayıcıların yalnızca Discord için yerel proxy'yi kullanması amacıyla sistem proxy (PAC) ayarını değiştirmek istiyor."
        let source = "do shell script \"\(escaped)\" with prompt \"\(prompt)\" with administrator privileges"
        var errInfo: NSDictionary?
        NSAppleScript(source: source)?.executeAndReturnError(&errInfo)
        if let errInfo {
            if (errInfo[NSAppleScript.errorNumber] as? Int) == -128 {
                throw SupportError.cancelled
            }
            let msg = errInfo[NSAppleScript.errorMessage] as? String ?? "bilinmeyen hata"
            throw SupportError.message(msg)
        }
    }
}

func shQuote(_ s: String) -> String {
    "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
}

/// 127.0.0.1 üzerinde PAC dosyasını sunan küçük HTTP sunucusu.
final class PACServer: @unchecked Sendable {
    private var listener: NWListener?
    private let queue = DispatchQueue(label: "pac-server")
    private var body = Data()

    func start(port: UInt16, script: String) throws {
        guard listener == nil else { return }
        body = Data(script.utf8)
        guard let p = NWEndpoint.Port(rawValue: port) else { throw NWError.posix(.EINVAL) }
        let params = NWParameters.tcp
        params.acceptLocalOnly = true
        params.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: p)
        let l = try NWListener(using: params)
        l.newConnectionHandler = { [weak self] conn in self?.serve(conn) }
        l.start(queue: queue)
        listener = l
    }

    private func serve(_ conn: NWConnection) {
        let body = self.body
        conn.start(queue: queue)
        conn.receive(minimumIncompleteLength: 1, maximumLength: 8192) { _, _, _, _ in
            let head = "HTTP/1.1 200 OK\r\nContent-Type: application/x-ns-proxy-autoconfig\r\nContent-Length: \(body.count)\r\nCache-Control: no-cache\r\nConnection: close\r\n\r\n"
            conn.send(content: Data(head.utf8) + body, completion: .contentProcessed { _ in conn.cancel() })
        }
    }
}
