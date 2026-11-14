@testable import AgenthailHelpers
import Foundation

final class CatalogStub: URLProtocol {
    nonisolated(unsafe) static var hits: [String] = []
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let url = request.url!
        CatalogStub.hits.append((request.httpMethod == "POST" ? "POST " : "") + "\(url.host ?? "")\(url.path)" + (url.query.map { "?" + $0 } ?? ""))
        let body = Data(#"{"host":"running","extensions":[{"id":"board","label":"Board","state":"loaded"}]}"#.utf8)
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@MainActor
func catalogSettles(_ catalog: ExtensionCatalog) async {
    for _ in 0..<200 where catalog.loading { try? await Task.sleep(nanoseconds: 10_000_000) }
}

@main
struct ExtensionEntriesTest {
    @MainActor
    static func main() async {
        let list = ExtensionsList(host: "running", hostError: nil, extensions: [
            ExtensionSummary(id: "board", label: "Board", description: "Who is working on what", state: "loaded", page: ExtensionPage(port: 7430, entry: "/", published: true), app: ExtensionAppOffer(label: "Team board", icon: "chart.bar", order: 2), badge: "3", mounts: [
                ExtensionMount(name: "console", label: "Console", port: 7421, entry: "/admin.html", state: "serving", published: true, app: ExtensionAppOffer(icon: "terminal", order: -1)),
                ExtensionMount(name: "local", label: "Local only", port: 7422, entry: "/", state: "serving", published: false),
                ExtensionMount(name: "waiting", label: "Waiting", entry: "/", state: "pending"),
            ]),
            ExtensionSummary(id: "broken", label: "Broken", state: "failed", error: "load failed: boom", page: ExtensionPage(port: 7431, entry: "/")),
            ExtensionSummary(id: "quiet", label: "Quiet", description: " quiet ", state: "loaded", page: ExtensionPage(port: 7432, entry: "/", published: false)),
        ])
        let local = ExtensionEntries.build(list, hostID: "h1", hostName: "studio", origin: URL(string: "http://127.0.0.1:7412")!)
        let page = local.first { $0.id == "h1/board/page" }
        check(page?.url?.absoluteString == "http://127.0.0.1:7430/", "a loaded page opens on its own port, got \(String(describing: page?.url))")
        check(page?.label == "Team board" && page?.icon == "chart.bar" && page?.order == 2 && page?.offered == true && page?.badge == "3", "the page carries what it offers: \(String(describing: page))")
        check(page?.detail == "Who is working on what", "a page's detail is its description, got \(String(describing: page?.detail))")
        check(local.first { $0.id == "h1/quiet/page" }?.detail == nil, "a description that only repeats the label shows no detail")
        check(local.first { $0.id == "h1/broken/page" }?.detail == nil, "a page without a description shows no detail")
        let console = local.first { $0.id == "h1/board/mount/console" }
        check(console?.detail == "Board", "a mount's detail names its extension")
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
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [CatalogStub.self]
        let stubbed = URLSession(configuration: config)
        let first = AgenthailAPI(baseURL: URL(string: "http://first.invalid")!, token: "t", session: stubbed)
        let second = AgenthailAPI(baseURL: URL(string: "http://second.invalid")!, token: "t", session: stubbed)
        let idle = ExtensionCatalog()
        idle.attach(first, hostID: "h1", hostName: "one")
        idle.attach(second, hostID: "h1", hostName: "one")
        await catalogSettles(idle)
        check(CatalogStub.hits.isEmpty, "a catalog nobody shows never fetches, got \(CatalogStub.hits)")
        let shown = ExtensionCatalog()
        shown.attach(first, hostID: "h1", hostName: "one")
        shown.refresh()
        await catalogSettles(shown)
        check(shown.list != nil, "the shown catalog loads")
        CatalogStub.hits = []
        shown.attach(second, hostID: "h1", hostName: "one")
        await catalogSettles(shown)
        check(CatalogStub.hits == ["second.invalid/api/v1/extensions"] && shown.list != nil, "a shown catalog refetches once from its new connection, got \(CatalogStub.hits)")
        CatalogStub.hits = []
        let undone: Bool
        do { try await first.undoExtensionChange("abc123"); undone = true } catch { undone = false }
        check(undone && CatalogStub.hits == ["POST first.invalid/api/v1/extensions/history/abc123/undo"], "undo posts to the change's own route, got \(CatalogStub.hits)")
        check(AgenthailAPI.undoPath("a/b?c") == "/api/v1/extensions/history/a%2Fb%3Fc/undo", "a revision stays one path segment, got \(AgenthailAPI.undoPath("a/b?c"))")
        CatalogStub.hits = []
        let paired = ExtensionCatalog(hostID: "h 2", hostName: "build", api: first, hostRef: "h 2&x")
        paired.refresh()
        await catalogSettles(paired)
        check(CatalogStub.hits == ["first.invalid/api/v1/extensions?host=h%202%26x"] && paired.list?.extensions.first?.id == "board", "a paired host's catalog asks this host for that host's list, got \(CatalogStub.hits)")
        check(AgenthailAPI.onHost("/api/v1/extensions/history?limit=5", "h2") == "/api/v1/extensions/history?limit=5&host=h2" && AgenthailAPI.onHost("/x", nil) == "/x", "the host joins an existing query")

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
