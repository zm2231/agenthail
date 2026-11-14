import CryptoKit
import Foundation
import Observation
import WebKit

protocol ExtensionSessionSource: Sendable {
    func session(fresh: Bool) async throws -> ExtensionWebSession
}

actor ExtensionSignIn: ExtensionSessionSource {
    static let renewBefore: TimeInterval = 60 * 60
    private let api: AgenthailAPI
    private var held: ExtensionWebSession?

    init(api: AgenthailAPI) { self.api = api }

    func session(fresh: Bool) async throws -> ExtensionWebSession {
        try await session(fresh: fresh, now: Date())
    }

    func session(fresh: Bool, now: Date) async throws -> ExtensionWebSession {
        if !fresh, let held, held.expiresAt.timeIntervalSince(now) > Self.renewBefore { return held }
        let minted = try await api.extensionWebSession()
        held = minted
        return minted
    }

    static func cookie(_ session: ExtensionWebSession, for origin: URL) -> HTTPCookie? {
        guard let host = origin.host else { return nil }
        var properties: [HTTPCookiePropertyKey: Any] = [
            .name: session.name, .value: session.value, .domain: host, .path: session.path,
            .expires: session.expiresAt, .sameSitePolicy: HTTPCookieStringPolicy.sameSiteStrict,
            HTTPCookiePropertyKey("HttpOnly"): "TRUE",
        ]
        if origin.scheme == "https" { properties[.secure] = "TRUE" }
        return HTTPCookie(properties: properties)
    }
}

@MainActor
enum ExtensionWebStores {
    private static var stores: [String: WKWebsiteDataStore] = [:]

    static func store(forHost hostID: String) -> WKWebsiteDataStore {
        if let store = stores[hostID] { return store }
        let store = WKWebsiteDataStore(forIdentifier: identifier(forHost: hostID))
        stores[hostID] = store
        return store
    }

    static func identifier(forHost hostID: String) -> UUID {
        var bytes = Array(SHA256.hash(data: Data(("agenthail-extensions:" + hostID).utf8)).prefix(16))
        bytes[6] = (bytes[6] & 0x0F) | 0x50
        bytes[8] = (bytes[8] & 0x3F) | 0x80
        return UUID(uuid: (bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5], bytes[6], bytes[7], bytes[8], bytes[9], bytes[10], bytes[11], bytes[12], bytes[13], bytes[14], bytes[15]))
    }
}

enum ExtensionNavigation {
    enum Decision: Equatable {
        case allow
        case openOutside
        case refuse
    }

    // The web session cookie is scoped to a host, not a port, so another port on the page's host never loads in it.
    static func decide(_ target: URL?, page: URL, mainFrame: Bool) -> Decision {
        guard let target, let scheme = target.scheme?.lowercased() else { return .refuse }
        if ["about", "data", "blob"].contains(scheme) { return .allow }
        guard scheme == "http" || scheme == "https" else { return .refuse }
        if origin(target) == origin(page) { return .allow }
        if target.host?.lowercased() == page.host?.lowercased() { return .refuse }
        return mainFrame ? .openOutside : .allow
    }

    static func origin(_ url: URL) -> String {
        let scheme = url.scheme?.lowercased() ?? ""
        let port = url.port ?? (scheme == "https" ? 443 : scheme == "http" ? 80 : 0)
        return "\(scheme)://\(url.host?.lowercased() ?? ""):\(port)"
    }
}

@MainActor
@Observable
final class ExtensionPageController: NSObject, WKNavigationDelegate, WKUIDelegate {
    enum Phase: Equatable {
        case signingIn
        case loading
        case shown
        case failed(String)
    }

    let entryID: String
    private(set) var phase: Phase = .signingIn
    let webView: WKWebView
    @ObservationIgnored private(set) var target: URL?
    @ObservationIgnored private(set) var loadsStarted = 0
    @ObservationIgnored private var source: (any ExtensionSessionSource)?
    @ObservationIgnored private var retried = false
    @ObservationIgnored private var stopped = false
    @ObservationIgnored private var loadTask: Task<Void, Never>?
    @ObservationIgnored private var renewTask: Task<Void, Never>?
    @ObservationIgnored var openOutside: (URL) -> Void = { _ in }

    init(entryID: String, hostID: String) {
        self.entryID = entryID
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = ExtensionWebStores.store(forHost: hostID)
        webView = WKWebView(frame: .zero, configuration: configuration)
        super.init()
        webView.navigationDelegate = self
        webView.uiDelegate = self
        webView.allowsBackForwardNavigationGestures = true
    }

    func open(_ url: URL, signIn: (any ExtensionSessionSource)?) {
        source = signIn
        guard url != target, !stopped else { return }
        target = url
        start(fresh: false)
    }

    func reload() {
        guard target != nil, !stopped else { return }
        start(fresh: false)
    }

    func stop() {
        stopped = true
        loadTask?.cancel()
        renewTask?.cancel()
        webView.stopLoading()
    }

    private func start(fresh: Bool) {
        retried = fresh
        loadTask?.cancel()
        loadTask = Task { [weak self] in await self?.load(fresh: fresh) }
    }

    private func live(_ url: URL) -> Bool { !Task.isCancelled && !stopped && target == url }

    private func load(fresh: Bool) async {
        guard let url = target else { return }
        phase = .signingIn
        do {
            if let source {
                let session = try await source.session(fresh: fresh)
                guard live(url) else { return }
                try await setCookie(session, for: url)
                guard live(url) else { return }
                scheduleRenewal(at: session.expiresAt, for: url)
            }
            phase = .loading
            loadsStarted += 1
            webView.load(URLRequest(url: url))
        } catch {
            if live(url) { phase = .failed(error.localizedDescription) }
        }
    }

    private func setCookie(_ session: ExtensionWebSession, for url: URL) async throws {
        guard let cookie = ExtensionSignIn.cookie(session, for: url) else { throw URLError(.badURL) }
        await webView.configuration.websiteDataStore.httpCookieStore.setCookie(cookie)
    }

    private func scheduleRenewal(at expiry: Date, for url: URL) {
        renewTask?.cancel()
        let wait = max(60, expiry.timeIntervalSinceNow - ExtensionSignIn.renewBefore)
        renewTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(wait))
            guard let self, self.live(url), let source = self.source else { return }
            guard let session = try? await source.session(fresh: true), self.live(url) else { return }
            try? await self.setCookie(session, for: url)
            self.scheduleRenewal(at: session.expiresAt, for: url)
        }
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction) async -> WKNavigationActionPolicy {
        guard let page = target else { return .cancel }
        switch ExtensionNavigation.decide(navigationAction.request.url, page: page, mainFrame: navigationAction.targetFrame?.isMainFrame ?? true) {
        case .allow: return .allow
        case .openOutside:
            if let url = navigationAction.request.url { openOutside(url) }
            return .cancel
        case .refuse: return .cancel
        }
    }

    func webView(_ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration, for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures) -> WKWebView? {
        guard let page = target, let url = navigationAction.request.url else { return nil }
        switch ExtensionNavigation.decide(url, page: page, mainFrame: true) {
        case .allow: webView.load(navigationAction.request)
        case .openOutside: openOutside(url)
        case .refuse: break
        }
        return nil
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse) async -> WKNavigationResponsePolicy {
        guard navigationResponse.isForMainFrame, let response = navigationResponse.response as? HTTPURLResponse, response.statusCode == 401 else { return .allow }
        if !retried {
            start(fresh: true)
            return .cancel
        }
        phase = .failed("Agenthail could not sign this page in. Check that this device may read on its host.")
        return .cancel
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        phase = .shown
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        failed(error)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        failed(error)
    }

    private func failed(_ error: Error) {
        let failure = error as NSError
        if failure.code == NSURLErrorCancelled || (failure.domain == "WebKitErrorDomain" && failure.code == 102) { return }
        if case .failed = phase { return }
        phase = .failed(error.localizedDescription)
    }
}

@MainActor
enum ExtensionPages {
    static let kept = 6
    private static var controllers: [String: ExtensionPageController] = [:]
    private static var recent: [String] = []

    static func controller(for entry: ExtensionEntry) -> ExtensionPageController {
        recent.removeAll { $0 == entry.id }
        recent.append(entry.id)
        if let controller = controllers[entry.id] { return controller }
        let controller = ExtensionPageController(entryID: entry.id, hostID: entry.hostID)
        controllers[entry.id] = controller
        while recent.count > kept {
            let dropped = recent.removeFirst()
            controllers.removeValue(forKey: dropped)?.stop()
        }
        return controller
    }

    static func forget(_ entryID: String) {
        recent.removeAll { $0 == entryID }
        controllers.removeValue(forKey: entryID)?.stop()
    }
}
