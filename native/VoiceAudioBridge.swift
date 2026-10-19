import AVFAudio
import SwiftUI
import WebKit

@MainActor
protocol VoiceAudioClient: AnyObject {
    var onMessage: ((String, String) -> Void)? { get set }
    func load(_ request: URLRequest)
    func start() async throws
    func end()
    func answer(_ sdp: String) async throws
    func mute(_ muted: Bool)
    func close()
    func configureCall(id: UUID, name: String)
}

extension VoiceAudioClient {
    func configureCall(id: UUID, name: String) {}
}

struct VoiceMicrophoneDenied: LocalizedError {
    var errorDescription: String? {
#if os(macOS)
        "Microphone access is off for Agenthail. Turn it on in System Settings, Privacy & Security, Microphone, then call again."
#else
        "Microphone access is off. Enable it for Agenthail in Settings."
#endif
    }
}

#if os(macOS)
@MainActor
final class VoiceAudioBridge: NSObject, ObservableObject, VoiceAudioClient, WKScriptMessageHandler, WKUIDelegate, WKNavigationDelegate {
    let webView: WKWebView
    var onMessage: ((String, String) -> Void)?
    private var origin: URL?
    private var permissionGeneration = 0

    override init() {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        configuration.mediaTypesRequiringUserActionForPlayback = []
        webView = WKWebView(frame: .zero, configuration: configuration)
        super.init()
        configuration.userContentController.add(self, name: "voice")
        webView.uiDelegate = self
        webView.navigationDelegate = self
    }

    func load(_ request: URLRequest) {
        onMessage?("loading", "")
        origin = request.url
        webView.load(request)
    }

    func start() async throws {
        permissionGeneration += 1
        let generation = permissionGeneration
        let allowed = await withCheckedContinuation { continuation in
            AVAudioApplication.requestRecordPermission { continuation.resume(returning: $0) }
        }
        guard generation == permissionGeneration else { throw CancellationError() }
        guard allowed else { throw VoiceMicrophoneDenied() }
        try await webView.evaluateJavaScript("void window.agenthailVoice.start()")
    }

    func end() {
        permissionGeneration += 1
        webView.evaluateJavaScript("window.agenthailVoice?.end()", completionHandler: nil)
        webView.setMicrophoneCaptureState(.none)
    }

    func answer(_ sdp: String) async throws {
        let data = try JSONSerialization.data(withJSONObject: [sdp])
        guard let argument = String(data: data, encoding: .utf8) else { throw AgenthailAPIError.invalidResponse }
        _ = try await webView.evaluateJavaScript("void window.agenthailVoice.answer(\(argument)[0])")
    }

    func mute(_ muted: Bool) {
        webView.evaluateJavaScript("window.agenthailVoice?.mute(\(muted ? "true" : "false"))", completionHandler: nil)
    }

    func close() {
        end()
        webView.configuration.userContentController.removeScriptMessageHandler(forName: "voice")
        webView.stopLoading()
    }

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, sameOrigin(message.frameInfo.request.url),
              let body = message.body as? [String: String], let type = body["type"] else { return }
        if type == "ready" { return }
        onMessage?(type, body["value"] ?? "")
    }

    private func sameOrigin(_ url: URL?) -> Bool { Self.sameOrigin(url, as: origin) }

    static func sameOrigin(_ url: URL?, as origin: URL?) -> Bool {
        guard let url, let origin, VoiceAPI.secureOrigin(url) else { return false }
        return url.scheme == origin.scheme && url.host == origin.host
            && (url.port ?? VoiceAPI.defaultPort(url.scheme)) == (origin.port ?? VoiceAPI.defaultPort(origin.scheme))
    }

    static func allowsNavigation(to url: URL?, from origin: URL?, mainFrame: Bool) -> Bool {
        mainFrame && sameOrigin(url, as: origin) && url?.path == "/api/v1/voice/peer"
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction, decisionHandler: @escaping @MainActor @Sendable (WKNavigationActionPolicy) -> Void) {
        let mainFrame = navigationAction.targetFrame?.isMainFrame == true
        decisionHandler(Self.allowsNavigation(to: navigationAction.request.url, from: origin, mainFrame: mainFrame) ? .allow : .cancel)
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse, decisionHandler: @escaping @MainActor @Sendable (WKNavigationResponsePolicy) -> Void) {
        if navigationResponse.isForMainFrame, let response = navigationResponse.response as? HTTPURLResponse, !(200..<300).contains(response.statusCode) {
            onMessage?("unavailable", "Audio page request failed (\(response.statusCode)). Reopen Voice after checking the paired host.")
            decisionHandler(.cancel); return
        }
        decisionHandler(.allow)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        guard sameOrigin(webView.url) else { return }
        webView.evaluateJavaScript("typeof window.agenthailVoice === 'object'") { [weak self] value, error in
            if let error { self?.onMessage?("unavailable", "Audio page initialization: \(error.localizedDescription)") }
            else if value as? Bool == true { self?.onMessage?("ready", "") }
            else { self?.onMessage?("unavailable", "The audio page did not initialize. Reopen Voice after checking the host build.") }
        }
    }

    func webView(_ webView: WKWebView, requestMediaCapturePermissionFor origin: WKSecurityOrigin, initiatedByFrame frame: WKFrameInfo, type: WKMediaCaptureType, decisionHandler: @escaping @MainActor @Sendable (WKPermissionDecision) -> Void) {
        guard let expected = self.origin, origin.protocol == expected.scheme, origin.host == expected.host,
              (origin.port == 0 ? VoiceAPI.defaultPort(origin.protocol) : origin.port) == (expected.port ?? VoiceAPI.defaultPort(expected.scheme)),
              frame.isMainFrame, type == .microphone else {
            decisionHandler(.deny); return
        }
        decisionHandler(.grant)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) { onMessage?("unavailable", error.localizedDescription) }
    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) { onMessage?("unavailable", error.localizedDescription) }
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) { onMessage?("unavailable", "The audio process ended. Hang up and reopen Voice to reconnect.") }
}

struct VoiceAudioSurface: NSViewRepresentable {
    let bridge: VoiceAudioBridge
    func makeNSView(context: Context) -> WKWebView { bridge.webView }
    func updateNSView(_ nsView: WKWebView, context: Context) {}
}
#endif
