#if SNAPSHOT
import AppKit
import SwiftUI

// Yalnızca geliştirme için (-D SNAPSHOT): arayüzü ekrana çizmeden PNG'ye
// kaydeder. Dağıtılan uygulamaya dahil edilmez.
//
//   DiscordBypass --snapshot out.png            örnek verilerle
//   DiscordBypass --snapshot out.png --live 20  gerçek çekirdekle, 20 sn sonra
enum Snapshot {
    @MainActor
    static func handle() -> Bool {
        let args = CommandLine.arguments
        guard let i = args.firstIndex(of: "--snapshot"), i + 1 < args.count else { return false }
        let path = args[i + 1]

        if let j = args.firstIndex(of: "--live"), j + 1 < args.count, let secs = Double(args[j + 1]) {
            // Test sırasında Discord'a kesinlikle dokunma.
            Core.shared.launchDiscord = false
            BrowserSupport.shared.startServer()
            Core.shared.start()
            DispatchQueue.main.asyncAfter(deadline: .now() + secs) {
                render(path)
                Core.shared.stopAndWait()
                exit(0)
            }
            return true
        }

        for line in demoEvents {
            Core.shared.ingest(eventLine: Data(line.utf8))
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
            render(path)
            exit(0)
        }
        return true
    }

    @MainActor
    static func render(_ path: String) {
        for (suffix, name) in [("", NSAppearance.Name.aqua), ("-dark", .darkAqua)] {
            let view = ContentView(core: .shared, browser: .shared)
                .background(Color(nsColor: .windowBackgroundColor))
            let hosting = NSHostingView(rootView: view)
            hosting.appearance = NSAppearance(named: name)
            hosting.frame = NSRect(origin: .zero, size: hosting.fittingSize)
            let window = NSWindow(contentRect: hosting.frame, styleMask: .borderless, backing: .buffered, defer: false)
            window.appearance = NSAppearance(named: name)
            window.contentView = hosting
            hosting.layoutSubtreeIfNeeded()
            hosting.display()
            guard let rep = hosting.bitmapImageRepForCachingDisplay(in: hosting.bounds) else { continue }
            hosting.cacheDisplay(in: hosting.bounds, to: rep)
            let out = path.replacingOccurrences(of: ".png", with: "\(suffix).png")
            try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: out))
            print("yazıldı: \(out)")
        }
    }

    static let demoEvents: [String] = [
        #"{"type":"ready","mode":"tlsrec+split","proxy":"http://127.0.0.1:8899"}"#,
        #"{"type":"open","id":1,"host":"gateway.discord.gg","ip":"162.159.134.234","bypass":true,"mode":"tlsrec+split"}"#,
        #"{"type":"open","id":2,"host":"discord.com","ip":"162.159.136.232","bypass":true,"mode":"tlsrec+split"}"#,
        #"{"type":"close","id":2,"host":"discord.com","bypass":true,"up":5120,"down":184320}"#,
        #"{"type":"open","id":3,"host":"discord.com","ip":"162.159.136.232","bypass":true,"mode":"tlsrec+split"}"#,
        #"{"type":"open","id":4,"host":"cdn.discordapp.com","ip":"162.159.129.233","bypass":true,"mode":"tlsrec+split"}"#,
        #"{"type":"close","id":4,"host":"cdn.discordapp.com","bypass":true,"up":2100,"down":2457600}"#,
        #"{"type":"open","id":5,"host":"media.tenor.co","ip":"142.250.187.110","mode":"direkt"}"#,
        #"{"type":"close","id":5,"host":"media.tenor.co","up":900,"down":350000}"#,
        #"{"type":"open","id":6,"host":"discord-attachments-uploads-prd.storage.googleapis.com","ip":"142.250.187.123","mode":"direkt"}"#,
        #"{"type":"open","id":7,"host":"latency.discord.media","ip":"66.22.196.12","bypass":true,"mode":"tlsrec+split"}"#,
        #"{"type":"close","id":7,"host":"latency.discord.media","bypass":true,"up":517,"down":0}"#,
        #"{"type":"fail","id":8,"host":"status.discord.com","bypass":true,"error":"zaman aşımı (paketler düşürülüyor olabilir)"}"#,
    ]
}
#endif
