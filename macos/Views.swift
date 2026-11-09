import AppKit
import SwiftUI

enum HostFilter: String, CaseIterable, Identifiable {
    case all, discord, other, problems
    var id: String { rawValue }
}

enum PanelTab { case hosts, log }

struct ContentView: View {
    @ObservedObject var core: Core
    @ObservedObject var browser: BrowserSupport
    @State private var filter: HostFilter = .all
    @State private var tab: PanelTab = .hosts

    var body: some View {
        VStack(spacing: 0) {
            header
                .padding(.horizontal, 14)
                .padding(.top, 12)
                .padding(.bottom, 10)

            if let msg = core.discordMessage ?? core.notice ?? browser.message {
                Text(msg)
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 14)
                    .padding(.bottom, 8)
            }

            Divider()
            browserRow
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
            Divider()

            if tab == .hosts {
                hostSection
            } else {
                logSection
            }

            Divider()
            footer
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
        }
        .frame(width: 400)
    }

    // MARK: Başlık

    private var header: some View {
        HStack(spacing: 10) {
            Image(systemName: core.menuSymbol)
                .font(.system(size: 22))
                .foregroundStyle(statusColor)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 2) {
                Text("Discord Bypass")
                    .font(.headline)
                Text(statusText)
                    .font(.caption)
                    .foregroundStyle(isFailed ? Color.red : Color.secondary)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 8)
            Toggle("", isOn: Binding(
                get: { core.isOn },
                set: { $0 ? core.start() : core.stop() }
            ))
            .toggleStyle(.switch)
            .labelsHidden()
            .help(core.isOn ? "Bypass'ı kapat" : "Bypass'ı aç")
        }
    }

    private var isFailed: Bool {
        if case .failed = core.state { return true }
        return false
    }

    private var statusColor: Color {
        switch core.state {
        case .running: return .green
        case .starting: return .orange
        case .stopped: return .secondary
        case .failed: return .red
        }
    }

    private var statusText: String {
        switch core.state {
        case .stopped: return "Kapalı. Discord ve tarayıcılar doğrudan bağlanıyor."
        case .starting: return "Başlatılıyor, çalışan yöntem aranıyor…"
        case .running: return "Açık · \(core.mode) · 127.0.0.1:\(Settings.proxyPort)"
        case .failed(let msg): return msg
        }
    }

    // MARK: Tarayıcı desteği

    private var browserRow: some View {
        HStack(spacing: 10) {
            Image(systemName: "globe")
                .foregroundStyle(browser.installed ? Color.accentColor : Color.secondary)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 1) {
                Text("Tarayıcılar")
                    .font(.subheadline.weight(.medium))
                Text(browser.installed
                     ? "Kurulu: tarayıcılar Discord'u bu proxy'den açar"
                     : "Kurulu değil: yalnızca Discord uygulaması kullanıyor")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 8)
            Button(browser.installed ? "Kaldır" : "Kur") { browser.toggle() }
                .disabled(browser.busy)
                .help("Sistem proxy ayarına yalnızca Discord alan adlarını yönlendiren bir PAC adresi ekler. Bir kez yönetici şifresi sorar.")
        }
    }

    // MARK: Bağlantılar

    private var rows: [HostStat] {
        core.hosts.values
            .filter { h in
                switch filter {
                case .all: return true
                case .discord: return h.bypass
                case .other: return !h.bypass
                case .problems: return h.hasProblem
                }
            }
            .sorted { ($0.lastSeen, $1.host) > ($1.lastSeen, $0.host) }
    }

    private func count(_ f: HostFilter) -> Int {
        core.hosts.values.filter { h in
            switch f {
            case .all: return true
            case .discord: return h.bypass
            case .other: return !h.bypass
            case .problems: return h.hasProblem
            }
        }.count
    }

    private var hostSection: some View {
        VStack(spacing: 0) {
            clientsLine
                .padding(.horizontal, 14)
                .padding(.top, 8)

            Picker("", selection: $filter) {
                Text("Tümü \(count(.all))").tag(HostFilter.all)
                Text("Discord \(count(.discord))").tag(HostFilter.discord)
                Text("Diğer \(count(.other))").tag(HostFilter.other)
                Text("Sorun \(count(.problems))").tag(HostFilter.problems)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding(.horizontal, 14)
            .padding(.vertical, 8)

            Divider()

            ScrollView {
                LazyVStack(spacing: 0) {
                    if rows.isEmpty {
                        Text(emptyText)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                            .padding(24)
                    }
                    ForEach(rows) { h in
                        HostRow(host: h)
                        Divider().padding(.leading, 30)
                    }
                }
            }
            .frame(height: 300)
        }
    }

    private var emptyText: String {
        switch filter {
        case .problems: return "Sorunlu bağlantı yok."
        case .other: return "Discord dışında proxy'den geçen bir alan adı yok."
        default: return core.isOn ? "Henüz bağlantı yok." : "Bypass kapalı."
        }
    }

    private var clientsLine: some View {
        HStack(alignment: .firstTextBaseline, spacing: 4) {
            Text("Proxy'yi kullanan:")
                .foregroundStyle(.secondary)
            if core.clients.isEmpty {
                Text(core.state == .running ? "şu an kimse" : "-")
                    .foregroundStyle(.secondary)
            } else {
                Text(core.clients.map { "\($0.name) (\($0.connections))" }.joined(separator: ", "))
                    .lineLimit(2)
            }
            Spacer(minLength: 0)
        }
        .font(.caption)
    }

    // MARK: Kayıt

    private var logSection: some View {
        ScrollView {
            Text(core.log.reversed().joined(separator: "\n"))
                .font(.system(size: 10.5, design: .monospaced))
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(12)
        }
        .frame(height: 300 + 62)
    }

    // MARK: Alt kısım

    private var footer: some View {
        HStack(spacing: 8) {
            Picker("", selection: $tab) {
                Text("Bağlantılar").tag(PanelTab.hosts)
                Text("Kayıt").tag(PanelTab.log)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()

            Spacer()

            Button {
                core.relaunchDiscord()
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .disabled(core.state != .running)
            .help("Discord'u proxy ile yeniden başlat")

            Button {
                core.clearStats()
            } label: {
                Image(systemName: "trash")
            }
            .help("Listeyi temizle")

            Menu {
                Picker("Yöntem", selection: $core.modeSetting) {
                    ForEach(Core.modeOptions, id: \.self) { Text($0).tag($0) }
                }
                Toggle("Oturum açılınca uygulamayı başlat", isOn: $core.launchAtLogin)
                Toggle("Uygulama açılınca bypass'ı başlat", isOn: $core.autoStart)
                Toggle("Başlatınca Discord'u proxy ile aç", isOn: $core.launchDiscord)
                Divider()
                Button("Çık") { NSApp.terminate(nil) }
            } label: {
                Image(systemName: "gearshape")
            }
            .menuStyle(.borderlessButton)
            .menuIndicator(.hidden)
            .fixedSize()
            .help("Ayarlar")
        }
    }
}

struct HostRow: View {
    let host: HostStat

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Circle()
                .fill(dotColor)
                .frame(width: 7, height: 7)
                .padding(.top, 5)
                .frame(width: 14)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(host.host)
                        .font(.system(size: 12, weight: .medium))
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer(minLength: 4)
                    badge
                }
                TimelineView(.periodic(from: .now, by: 5)) { ctx in
                    Text(detail + ago(from: host.lastSeen, to: ctx.date))
                        .font(.system(size: 10.5))
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                        .lineLimit(1)
                }
                if host.hasProblem, let err = host.lastError {
                    Text(err)
                        .font(.system(size: 10.5))
                        .foregroundStyle(host.errors > 0 ? Color.red : Color.orange)
                        .lineLimit(2)
                }
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .help(host.ip.isEmpty ? host.host : "\(host.host) → \(host.ip)")
    }

    private var badge: some View {
        Text(host.bypass ? "BYPASS" : "DİREKT")
            .font(.system(size: 9, weight: .bold))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .foregroundStyle(host.bypass ? Color.white : Color.secondary)
            .background(
                Capsule().fill(host.bypass ? Color.accentColor : Color.secondary.opacity(0.18))
            )
            .help(host.bypass ? "DoH + ClientHello parçalama (\(host.mode))" : "Olduğu gibi iletiliyor")
    }

    private var dotColor: Color {
        if host.errors > 0 { return .red }
        if host.noReply > 0 { return .orange }
        return host.active > 0 ? .green : Color.secondary.opacity(0.5)
    }

    private var detail: String {
        var parts: [String] = []
        if host.active > 0 { parts.append("\(host.active) açık") }
        parts.append("\(host.total) bağlantı")
        parts.append("↑\(bytes(host.up)) ↓\(bytes(host.down))")
        return parts.joined(separator: " · ") + " · "
    }

    private func bytes(_ n: Int64) -> String {
        switch n {
        case ..<1000: return "\(n) B"
        case ..<1_000_000: return String(format: "%.0f KB", Double(n) / 1e3)
        case ..<1_000_000_000: return String(format: "%.1f MB", Double(n) / 1e6)
        default: return String(format: "%.2f GB", Double(n) / 1e9)
        }
    }

    private func ago(from date: Date, to now: Date) -> String {
        let s = Int(now.timeIntervalSince(date))
        switch s {
        case ..<5: return "şimdi"
        case ..<60: return "\(s) sn önce"
        case ..<3600: return "\(s / 60) dk önce"
        default: return "\(s / 3600) sa önce"
        }
    }
}
