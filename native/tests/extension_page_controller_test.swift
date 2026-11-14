@testable import AgenthailHelpers
import Foundation

actor SlowSessions: ExtensionSessionSource {
    private(set) var minted = 0
    private var waiting: [CheckedContinuation<Void, Never>] = []

    func session(fresh: Bool) async throws -> ExtensionWebSession {
        minted += 1
        await withCheckedContinuation { waiting.append($0) }
        return ExtensionWebSession(name: "agenthail_web", value: "ahw_test", path: "/", expiresAt: Date().addingTimeInterval(12 * 3600), scopes: ["read"])
    }

    func release() {
        waiting.forEach { $0.resume() }
        waiting.removeAll()
    }

    func count() -> Int { minted }
}

@main
struct ExtensionPageControllerTest {
    @MainActor
    static func main() async {
        let page = URL(string: "http://127.0.0.1:7412/x/board/")!
        typealias Nav = ExtensionNavigation
        check(Nav.decide(URL(string: "http://127.0.0.1:7412/x/board/assets/a.js"), page: page, mainFrame: false) == .allow, "the page's own origin loads")
        check(Nav.decide(URL(string: "http://127.0.0.1:7421/"), page: page, mainFrame: true) == .refuse, "another port on the page's host never loads in the signed-in view")
        check(Nav.decide(URL(string: "http://127.0.0.1:7421/"), page: page, mainFrame: false) == .refuse, "nor in a frame")
        check(Nav.decide(URL(string: "https://example.com/doc"), page: page, mainFrame: true) == .openOutside, "a link to another host opens outside the app")
        check(Nav.decide(URL(string: "https://cdn.example.com/x.js"), page: page, mainFrame: false) == .allow, "a frame from another host gets no cookie, so it loads")
        check(Nav.decide(URL(string: "file:///etc/hosts"), page: page, mainFrame: true) == .refuse, "other schemes are refused")
        check(Nav.decide(URL(string: "about:blank"), page: page, mainFrame: false) == .allow, "about:blank loads")
        check(Nav.decide(URL(string: "http://127.0.0.1:80/"), page: URL(string: "http://127.0.0.1/x/b/")!, mainFrame: true) == .allow, "a default port matches an omitted one")
        check(Nav.decide(URL(string: "https://host.example.ts.net:7421/"), page: URL(string: "https://host.example.ts.net:7412/x/b/")!, mainFrame: true) == .refuse, "over the tailnet a mount port is refused too")

        let sessions = SlowSessions()
        let controller = ExtensionPageController(entryID: "test/once", hostID: "test-host")
        let dead = URL(string: "http://127.0.0.1:9/x/board/")!
        for _ in 0..<5 { controller.open(dead, signIn: sessions) }
        try? await Task.sleep(for: .milliseconds(100))
        check(await sessions.count() == 1, "repeated opens of the same page mint once, got \(await sessions.count())")
        await sessions.release()
        try? await Task.sleep(for: .milliseconds(300))
        for _ in 0..<5 { controller.open(dead, signIn: sessions) }
        try? await Task.sleep(for: .milliseconds(100))
        check(controller.loadsStarted == 1, "repeated opens start one load, got \(controller.loadsStarted)")
        check(await sessions.count() == 1, "and no second mint, got \(await sessions.count())")
        controller.reload()
        try? await Task.sleep(for: .milliseconds(100))
        await sessions.release()
        try? await Task.sleep(for: .milliseconds(300))
        check(controller.loadsStarted == 2, "Reload starts a load, got \(controller.loadsStarted)")

        let stopping = SlowSessions()
        let evicted = ExtensionPageController(entryID: "test/stop", hostID: "test-host")
        evicted.open(dead, signIn: stopping)
        try? await Task.sleep(for: .milliseconds(100))
        evicted.stop()
        await stopping.release()
        try? await Task.sleep(for: .milliseconds(300))
        check(evicted.loadsStarted == 0, "a controller stopped while signing in loads nothing, got \(evicted.loadsStarted)")
        evicted.open(URL(string: "http://127.0.0.1:9/other/")!, signIn: stopping)
        try? await Task.sleep(for: .milliseconds(100))
        check(await stopping.count() == 1, "a stopped controller opens nothing more")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
