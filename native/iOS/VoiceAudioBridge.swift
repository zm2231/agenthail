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
}

@MainActor
final class VoiceAudioBridge: NSObject, ObservableObject, VoiceAudioClient, WKScriptMessageHandler, WKUIDelegate, WKNavigationDelegate {
    let webView: WKWebView
    var onMessage: ((String, String) -> Void)?
    private var origin: URL?
    private var permissionGeneration = 0

    override init() {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        configuration.allowsInlineMediaPlayback = true
        configuration.mediaTypesRequiringUserActionForPlayback = []
        webView = WKWebView(frame: .zero, configuration: configuration)
        super.init()
        configuration.userContentController.add(self, name: "voice")
        webView.uiDelegate = self
        webView.navigationDelegate = self
        webView.isOpaque = false
        webView.backgroundColor = .clear
    }

    func load(_ request: URLRequest) {
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
        guard allowed else { throw AgenthailAPIError.unavailable("Microphone access is off. Enable it for Agenthail in Settings.") }
        try AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .voiceChat, options: [.defaultToSpeaker, .allowBluetoothHFP])
        try AVAudioSession.sharedInstance().setActive(true)
        try await webView.evaluateJavaScript("void window.agenthailVoice.start()")
    }

    func end() {
        permissionGeneration += 1
        webView.evaluateJavaScript("window.agenthailVoice?.end()", completionHandler: nil)
        webView.setMicrophoneCaptureState(.none)
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
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
        onMessage?(type, body["value"] ?? "")
    }

    private func sameOrigin(_ url: URL?) -> Bool {
        guard let url, let origin else { return false }
        return url.scheme == "https" && url.host == origin.host && (url.port ?? 443) == (origin.port ?? 443)
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction, decisionHandler: @escaping @MainActor @Sendable (WKNavigationActionPolicy) -> Void) {
        decisionHandler(sameOrigin(navigationAction.request.url) && navigationAction.request.url?.path == "/api/v1/voice/peer" ? .allow : .cancel)
    }

    func webView(_ webView: WKWebView, requestMediaCapturePermissionFor origin: WKSecurityOrigin, initiatedByFrame frame: WKFrameInfo, type: WKMediaCaptureType, decisionHandler: @escaping @MainActor @Sendable (WKPermissionDecision) -> Void) {
        guard let expected = self.origin, origin.protocol == "https", origin.host == expected.host,
              (origin.port == 0 ? 443 : origin.port) == (expected.port ?? 443), frame.isMainFrame, type == .microphone else {
            decisionHandler(.deny); return
        }
        decisionHandler(.grant)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) { onMessage?("error", error.localizedDescription) }
    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) { onMessage?("error", error.localizedDescription) }
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) { onMessage?("error", "The audio process ended. Hang up and reopen Voice to reconnect.") }
}

struct VoiceAudioSurface: UIViewRepresentable {
    let bridge: VoiceAudioBridge
    func makeUIView(context: Context) -> WKWebView { bridge.webView }
    func updateUIView(_ uiView: WKWebView, context: Context) {}
}
