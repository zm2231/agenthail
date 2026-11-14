@testable import AgenthailHelpers
import Foundation

@main
struct ExtensionEntriesTest {
    @MainActor
    static func main() async {
        let list = ExtensionsList(host: "running", hostError: nil, extensions: [
            ExtensionSummary(id: "board", label: "Board", state: "loaded", page: ExtensionPage(port: 7430, entry: "/", published: true), app: ExtensionAppOffer(label: "Team board", icon: "chart.bar", order: 2), badge: "3", mounts: [
                ExtensionMount(name: "console", label: "Console", port: 7421, entry: "/admin.html", state: "serving", published: true, app: ExtensionAppOffer(icon: "terminal", order: -1)),
                ExtensionMount(name: "local", label: "Local only", port: 7422, entry: "/", state: "serving", published: false),
                ExtensionMount(name: "waiting", label: "Waiting", entry: "/", state: "pending"),
            ]),
            ExtensionSummary(id: "broken", label: "Broken", state: "failed", error: "load failed: boom", page: ExtensionPage(port: 7431, entry: "/")),
            ExtensionSummary(id: "quiet", label: "Quiet", state: "loaded", page: ExtensionPage(port: 7432, entry: "/", published: false)),
        ])
        let local = ExtensionEntries.build(list, hostID: "h1", hostName: "studio", origin: URL(string: "http://127.0.0.1:7412")!)
        let page = local.first { $0.id == "h1/board/page" }
        check(page?.url?.absoluteString == "http://127.0.0.1:7430/", "a loaded page opens on its own port, got \(String(describing: page?.url))")
        check(page?.label == "Team board" && page?.icon == "chart.bar" && page?.order == 2 && page?.offered == true && page?.badge == "3", "the page carries what it offers: \(String(describing: page))")
        let console = local.first { $0.id == "h1/board/mount/console" }
        check(console?.url?.absoluteString == "http://127.0.0.1:7421/admin.html", "a serving mount opens on its own port, got \(String(describing: console?.url))")
        check(console?.label == "Console" && console?.icon == "terminal", "a mount offer without a label keeps the mount's label")
        check(local.first { $0.id == "h1/board/mount/local" }?.url != nil, "over http an unpublished mount still opens")
        let waiting = local.first { $0.id == "h1/board/mount/waiting" }
        check(waiting?.url == nil && waiting?.unavailable?.contains("approval on studio") == true, "a pending mount says it waits for approval, got \(String(describing: waiting?.unavailable))")
        let broken = local.first { $0.id == "h1/broken/page" }
        check(broken?.url == nil && broken?.unavailable == "load failed: boom" && broken?.icon == ExtensionEntry.fallbackIcon && broken?.offered == false, "a failed page shows its error and no link")
        check(local.map(\.extensionID) == ["board", "board", "board", "board", "broken", "quiet"] && local.first?.kind == .page, "the hub groups by extension, page first, got \(local.map(\.id))")
        check(local.first { $0.id == "h1/quiet/page" }?.url?.absoluteString == "http://127.0.0.1:7432/", "over http an unpublished page still opens")

        let remote = ExtensionEntries.build(list, hostID: "h2", hostName: "build", origin: URL(string: "https://build.example.ts.net:7412")!)
        check(remote.first { $0.id == "h2/board/mount/console" }?.url?.absoluteString == "https://build.example.ts.net:7421/admin.html", "a published mount opens on the host's tailnet name")
        let hidden = remote.first { $0.id == "h2/board/mount/local" }
        check(hidden?.url == nil && hidden?.unavailable == "build doesn't publish this on the tailnet.", "over https an unpublished mount cannot open, got \(String(describing: hidden?.unavailable))")
        check(remote.first { $0.id == "h2/quiet/page" }?.url == nil, "over https an unpublished page cannot open")
        check(remote.first { $0.id == "h2/board/page" }?.url?.absoluteString == "https://build.example.ts.net:7430/", "a published page opens on the host's tailnet name")

        let pinned = ExtensionEntries.pinned(local, pins: ["h1/board/page", "h1/board/mount/console", "h9/gone/page"])
        check(pinned.map(\.id) == ["h1/board/mount/console", "h1/board/page"], "pins follow the offered order and skip entries that are gone, got \(pinned.map(\.id))")

        let defaults = UserDefaults(suiteName: "extension-entries-test-\(UUID().uuidString)")!
        let pins = ExtensionPins(defaults: defaults)
        pins.toggle(local[0])
        check(ExtensionPins(defaults: defaults).isPinned(local[0]), "a pin survives a new store")
        pins.toggle(local[0])
        check(!ExtensionPins(defaults: defaults).isPinned(local[0]), "an unpin survives a new store")

        let session = ExtensionWebSession(name: "agenthail_web", value: "ahw_x", path: "/", expiresAt: Date().addingTimeInterval(3600), scopes: ["read"])
        let plain = ExtensionSignIn.cookie(session, for: URL(string: "http://127.0.0.1:7430/")!)
        check(plain?.domain == "127.0.0.1" && plain?.isSecure == false && plain?.isHTTPOnly == true && plain?.sameSitePolicy == .sameSiteStrict, "the cookie is for the page's host, HttpOnly and strict: \(String(describing: plain))")
        check(ExtensionSignIn.cookie(session, for: URL(string: "https://build.example.ts.net:7421/")!)?.isSecure == true, "over https the cookie is Secure")

        let id = ExtensionWebStores.identifier(forHost: "h1")
        check(id == ExtensionWebStores.identifier(forHost: "h1") && id != ExtensionWebStores.identifier(forHost: "h2"), "each host keeps one web data store")

        let decoded = try? ExtensionDates.decoder().decode(ExtensionChanges.self, from: Data(#"{"changes":[{"rev":"abc","time":"2026-11-13T22:00:00.123456789Z","extensions":[{"id":"board","updated":["ui.html"]}]},{"rev":"root","time":"2026-11-12T10:00:00Z","root":true}]}"#.utf8))
        check(decoded?.changes.count == 2 && decoded?.changes.first?.touches("board") == true, "history decodes with fractional and whole-second times")
        if let change = decoded?.changes.first {
            check(ExtensionHistory.describe(change) == "changed 1 file in board", "a change reads as what it did, got \(ExtensionHistory.describe(change))")
        }
        check(ExtensionCatalog.refreshes(for: "extension.badge") && ExtensionCatalog.refreshes(for: "extension.changed") && !ExtensionCatalog.refreshes(for: "session.updated"), "the list rereads on extension events only")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
