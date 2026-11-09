import AppKit
import Foundation
import ServiceManagement

// Core, Go çekirdeğini (discord-bypass run -ui) alt süreç olarak çalıştırır,
// stdout'tan gelen JSON olaylarını okur ve host bazında istatistik tutar.

enum CoreState: Equatable {
    case stopped
    case starting
    case running
    case failed(String)
}

struct HostStat: Identifiable {
    let host: String
    var id: String { host }
    var bypass = false
    var mode = ""
    var ip = ""
    var total = 0
    var active = 0
    var errors = 0
    var noReply = 0
    var up: Int64 = 0
    var down: Int64 = 0
    var lastSeen = Date()
    var lastError: String?

    var hasProblem: Bool { errors > 0 || noReply > 0 }
}

struct ClientApp: Identifiable {
    let name: String
    let connections: Int
    var id: String { name }
}

struct CoreEvent: Decodable {
    let type: String
    var id: UInt64?
    var host: String?
    var ip: String?
    var bypass: Bool?
    var mode: String?
    var up: Int64?
    var down: Int64?
    var ok: Bool?
    var error: String?
}

enum Settings {
    static var defaults: UserDefaults { .standard }
    // Komut satırından da verilebilir: -proxyPort 18899 (UserDefaults argüman alanı).
    static var proxyPort: Int { int("proxyPort", 8899) }
    static var pacPort: Int { int("pacPort", 8898) }

    static func int(_ key: String, _ fallback: Int) -> Int {
        let v = defaults.integer(forKey: key)
        return v > 0 && v < 65536 ? v : fallback
    }

    // bool(forKey:) hem kayıtlı değerleri hem "-anahtar NO" gibi argümanları okur.
    static func bool(_ key: String, _ fallback: Bool) -> Bool {
        defaults.object(forKey: key) == nil ? fallback : defaults.bool(forKey: key)
    }
}

@MainActor
final class Core: ObservableObject {
    static let shared = Core()

    static let modeOptions = ["auto", "tlsrec+split", "tlsrec", "split", "disorder", "none"]

    @Published private(set) var state: CoreState = .stopped
    @Published private(set) var mode = ""
    @Published private(set) var hosts: [String: HostStat] = [:]
    @Published private(set) var log: [String] = []
    @Published private(set) var clients: [ClientApp] = []
    @Published private(set) var discordMessage: String?
    @Published private(set) var notice: String?

    @Published var modeSetting: String {
        didSet {
            Settings.defaults.set(modeSetting, forKey: "mode")
            if oldValue != modeSetting { restart() }
        }
    }
    @Published var launchDiscord: Bool {
        didSet { Settings.defaults.set(launchDiscord, forKey: "launchDiscord") }
    }
    @Published var autoStart: Bool {
        didSet { Settings.defaults.set(autoStart, forKey: "autoStart") }
    }
    /// Oturum açılınca uygulamayı başlat (Sistem Ayarları → Genel → Giriş Öğeleri).
    @Published var launchAtLogin = SMAppService.mainApp.status == .enabled {
        didSet {
            guard launchAtLogin != (SMAppService.mainApp.status == .enabled) else { return }
            do {
                if launchAtLogin {
                    try SMAppService.mainApp.register()
                } else {
                    try SMAppService.mainApp.unregister()
                }
                notice = nil
            } catch {
                notice = "Giriş öğesi ayarlanamadı: \(error.localizedDescription). Uygulamayı Applications klasörüne taşıyıp tekrar deneyin."
                launchAtLogin = SMAppService.mainApp.status == .enabled
            }
        }
    }

    private var process: Process?
    private var commandPipe: FileHandle?
    private var stopRequested = false
    private var restartPending = false
    private var lastError = ""
    private var openConns: [UInt64: String] = [:]
    private var outBuffer = Data()
    private var errBuffer = Data()
    private var clientTimer: Timer?

    private init() {
        let d = Settings.defaults
        modeSetting = d.string(forKey: "mode") ?? "auto"
        launchDiscord = Settings.bool("launchDiscord", true)
        autoStart = Settings.bool("autoStart", true)
    }

    var isOn: Bool { state == .running || state == .starting }

    var menuSymbol: String {
        switch state {
        case .running: return "bolt.shield.fill"
        case .starting: return "bolt.shield"
        case .stopped: return "shield.slash"
        case .failed: return "exclamationmark.shield"
        }
    }

    static func coreExecutable() -> URL? {
        if let url = Bundle.main.url(forAuxiliaryExecutable: "discord-bypass") {
            return url
        }
        let sibling = Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent("discord-bypass")
        if let sibling, FileManager.default.isExecutableFile(atPath: sibling.path) {
            return sibling
        }
        return nil
    }

    // MARK: - Süreç yönetimi

    func start() {
        guard process == nil else { return }
        guard let exe = Core.coreExecutable() else {
            state = .failed("Çekirdek (discord-bypass) uygulama paketinde bulunamadı.")
            return
        }

        let p = Process()
        p.executableURL = exe
        p.arguments = ["run", "-ui",
                       "-port", String(Settings.proxyPort),
                       "-mode", modeSetting,
                       "-launch=\(launchDiscord)"]
        let out = Pipe(), err = Pipe(), inp = Pipe()
        p.standardOutput = out
        p.standardError = err
        p.standardInput = inp

        out.fileHandleForReading.readabilityHandler = { [weak self] h in
            let data = h.availableData
            if data.isEmpty { h.readabilityHandler = nil; return }
            DispatchQueue.main.async { self?.consume(data, isErr: false) }
        }
        err.fileHandleForReading.readabilityHandler = { [weak self] h in
            let data = h.availableData
            if data.isEmpty { h.readabilityHandler = nil; return }
            DispatchQueue.main.async { self?.consume(data, isErr: true) }
        }
        p.terminationHandler = { [weak self] proc in
            let status = proc.terminationStatus
            DispatchQueue.main.async { self?.terminated(status: status) }
        }

        do {
            try p.run()
        } catch {
            state = .failed("Çekirdek başlatılamadı: \(error.localizedDescription)")
            return
        }
        process = p
        commandPipe = inp.fileHandleForWriting
        stopRequested = false
        lastError = ""
        discordMessage = nil
        outBuffer.removeAll()
        errBuffer.removeAll()
        state = .starting
        startClientMonitor()
    }

    func stop() {
        guard let p = process else { return }
        stopRequested = true
        p.terminate()
    }

    func restart() {
        guard process != nil else { return }
        restartPending = true
        stop()
    }

    /// Uygulama kapanırken çağrılır; çekirdeğin gerçekten kapandığını bekler.
    func stopAndWait() {
        guard let p = process else { return }
        stopRequested = true
        p.terminate()
        let deadline = Date().addingTimeInterval(2)
        while p.isRunning && Date() < deadline { usleep(50_000) }
        if p.isRunning { kill(p.processIdentifier, SIGKILL) }
    }

    func relaunchDiscord() {
        guard let h = commandPipe else { return }
        try? h.write(contentsOf: Data("relaunch\n".utf8))
        discordMessage = nil
    }

    func clearStats() {
        hosts = hosts.filter { $0.value.active > 0 }.mapValues { h in
            var c = HostStat(host: h.host)
            c.bypass = h.bypass; c.mode = h.mode; c.ip = h.ip; c.active = h.active; c.total = h.active
            return c
        }
        log.removeAll()
    }

    private func terminated(status: Int32) {
        process = nil
        commandPipe = nil
        openConns.removeAll()
        for key in hosts.keys { hosts[key]?.active = 0 }
        clients = []
        stopClientMonitor()

        if stopRequested || status == 0 {
            state = .stopped
        } else {
            state = .failed(lastError.isEmpty ? "Çekirdek beklenmedik şekilde kapandı (kod \(status))." : lastError)
        }
        appendLog("çekirdek durdu")
        if restartPending {
            restartPending = false
            start()
        }
    }

    // MARK: - Olay işleme

    private func consume(_ data: Data, isErr: Bool) {
        var buf = isErr ? errBuffer : outBuffer
        buf.append(data)
        while let nl = buf.firstIndex(of: 0x0A) {
            let line = Data(buf[buf.startIndex..<nl])
            buf.removeSubrange(buf.startIndex...nl)
            if isErr {
                handleLogLine(String(decoding: line, as: UTF8.self))
            } else {
                ingest(eventLine: line)
            }
        }
        if isErr { errBuffer = buf } else { outBuffer = buf }
    }

    private func handleLogLine(_ line: String) {
        guard !line.isEmpty else { return }
        appendLog(line)
        if let r = line.range(of: "hata: ") {
            lastError = String(line[r.upperBound...])
        }
    }

    private func appendLog(_ line: String) {
        log.append(line)
        if log.count > 300 { log.removeFirst(log.count - 300) }
    }

    func ingest(eventLine: Data) {
        guard let e = try? JSONDecoder().decode(CoreEvent.self, from: eventLine) else { return }
        switch e.type {
        case "ready":
            state = .running
            mode = e.mode ?? ""
        case "discord":
            discordMessage = (e.ok ?? false) ? nil : "Discord başlatılamadı: \(e.error ?? "bilinmeyen hata")"
        case "open":
            guard let id = e.id, let host = e.host else { return }
            openConns[id] = host
            var h = hosts[host] ?? HostStat(host: host)
            h.total += 1
            h.active += 1
            h.bypass = e.bypass ?? false
            h.mode = e.mode ?? ""
            h.ip = e.ip ?? h.ip
            h.lastSeen = Date()
            hosts[host] = h
        case "close":
            guard let id = e.id else { return }
            let host = openConns.removeValue(forKey: id) ?? e.host ?? ""
            guard var h = hosts[host] else { return }
            let up = e.up ?? 0, down = e.down ?? 0
            h.active = max(0, h.active - 1)
            h.up += up
            h.down += down
            h.lastSeen = Date()
            if let err = e.error, !err.isEmpty {
                h.errors += 1
                h.lastError = err
            } else if h.bypass && up > 0 && down == 0 {
                // ClientHello gitti ama hiç yanıt gelmedi: engellenmiş olabilir.
                h.noReply += 1
                h.lastError = "ClientHello gönderildi, sunucudan yanıt gelmedi"
            }
            hosts[host] = h
        case "fail":
            guard let host = e.host else { return }
            var h = hosts[host] ?? HostStat(host: host)
            h.total += 1
            h.errors += 1
            h.bypass = e.bypass ?? h.bypass
            h.lastError = "bağlanılamadı: \(e.error ?? "")"
            h.lastSeen = Date()
            hosts[host] = h
        case "error":
            if let err = e.error { lastError = err }
        default:
            break
        }
    }

    // MARK: - Proxy'yi kullanan uygulamalar

    private func startClientMonitor() {
        stopClientMonitor()
        clientTimer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            DispatchQueue.main.async { self?.refreshClients() }
        }
    }

    private func stopClientMonitor() {
        clientTimer?.invalidate()
        clientTimer = nil
    }

    private func refreshClients() {
        guard state == .running, let pid = process?.processIdentifier else { return }
        let port = Settings.proxyPort
        DispatchQueue.global(qos: .utility).async { [weak self] in
            let list = Core.listClients(port: port, excluding: pid)
            DispatchQueue.main.async { self?.clients = list }
        }
    }

    /// lsof ile proxy portuna bağlı süreçleri bulur (yalnızca bu kullanıcının süreçleri).
    nonisolated static func listClients(port: Int, excluding: Int32) -> [ClientApp] {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/sbin/lsof")
        p.arguments = ["+c", "0", "-nP", "-iTCP@127.0.0.1:\(port)", "-sTCP:ESTABLISHED", "-Fpcn"]
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = FileHandle.nullDevice
        guard (try? p.run()) != nil else { return [] }
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        p.waitUntilExit()

        var pid: Int32 = 0
        var name = ""
        var counts: [String: Int] = [:]
        for line in String(decoding: data, as: UTF8.self).split(separator: "\n") {
            guard let tag = line.first else { continue }
            let value = String(line.dropFirst())
            switch tag {
            case "p": pid = Int32(value) ?? 0
            case "c": name = prettyName(value)
            case "n": if pid != excluding { counts[name, default: 0] += 1 }
            default: break
            }
        }
        return counts.map { ClientApp(name: $0.key, connections: $0.value) }
            .sorted { $0.connections > $1.connections }
    }

    nonisolated static func prettyName(_ s: String) -> String {
        if s.contains("WebKit.Networking") { return "Safari (WebKit)" }
        return s
    }
}
