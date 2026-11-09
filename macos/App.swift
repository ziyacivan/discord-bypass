import AppKit
import SwiftUI

@main
struct DiscordBypassApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var core = Core.shared
    @StateObject private var browser = BrowserSupport.shared

    var body: some Scene {
        MenuBarExtra {
            ContentView(core: core, browser: browser)
        } label: {
            Image(systemName: core.menuSymbol)
        }
        .menuBarExtraStyle(.window)
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        // Çekirdek kapandıktan sonra ona yazmak uygulamayı öldürmesin.
        signal(SIGPIPE, SIG_IGN)
        #if SNAPSHOT
        if Snapshot.handle() { return }
        #endif
        BrowserSupport.shared.startServer()
        if Core.shared.autoStart {
            Core.shared.start()
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        Core.shared.stopAndWait()
    }
}
