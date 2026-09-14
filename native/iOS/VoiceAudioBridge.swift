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
        onMessage?("loading", "")
        origin = request.url
        webView.load(request)
    }

    func start() async throws {
#if DEBUG && targetEnvironment(simulator)
        if VoiceEvaluation.enabled {
            let ready = try await webView.evaluateJavaScript("window.agenthailEvaluationReady?.() === true")
            guard ready as? Bool == true else { throw AgenthailAPIError.unavailable("Simulator speech injection did not initialize. Native microphone capture is disabled for this evaluation.") }
            try await webView.evaluateJavaScript("void window.agenthailVoice.start()")
            return
        }
#endif
        permissionGeneration += 1
        let generation = permissionGeneration
        let allowed = await withCheckedContinuation { continuation in
            AVAudioApplication.requestRecordPermission { continuation.resume(returning: $0) }
        }
        guard generation == permissionGeneration else { throw CancellationError() }
        guard allowed else { throw AgenthailAPIError.unavailable("Microphone access is off. Enable it for Agenthail in Settings.") }
        try await webView.evaluateJavaScript("void window.agenthailVoice.start()")
    }

    func end() {
        permissionGeneration += 1
#if DEBUG && targetEnvironment(simulator)
        if VoiceEvaluation.enabled { webView.evaluateJavaScript("window.agenthailEvaluationRecord?.()", completionHandler: nil) }
#endif
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
#if DEBUG && targetEnvironment(simulator)
        if type == "evaluationDiagnostic" { VoiceEvaluation.saveDiagnostic(body["value"] ?? ""); return }
        if type == "evaluationRecording" { VoiceEvaluation.saveRecording(body["value"] ?? ""); return }
#endif
        onMessage?(type, body["value"] ?? "")
    }

    private func sameOrigin(_ url: URL?) -> Bool {
        guard let url, let origin else { return false }
        return url.scheme == "https" && url.host == origin.host && (url.port ?? 443) == (origin.port ?? 443)
    }

    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction, decisionHandler: @escaping @MainActor @Sendable (WKNavigationActionPolicy) -> Void) {
        decisionHandler(sameOrigin(navigationAction.request.url) && navigationAction.request.url?.path == "/api/v1/voice/peer" ? .allow : .cancel)
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
        var script = "typeof window.agenthailVoice === 'object'"
#if DEBUG && targetEnvironment(simulator)
        if VoiceEvaluation.enabled { script = VoiceEvaluation.script + "\n typeof window.agenthailVoice === 'object' && window.agenthailEvaluationReady?.() === true" }
#endif
        webView.evaluateJavaScript(script) { [weak self] value, error in
            if let error { self?.onMessage?("unavailable", "Audio page initialization: \(error.localizedDescription)") }
            else if value as? Bool == true { self?.onMessage?("ready", "") }
            else { self?.onMessage?("unavailable", "The audio page did not initialize. Reopen Voice after checking the host build.") }
        }
    }

    func webView(_ webView: WKWebView, requestMediaCapturePermissionFor origin: WKSecurityOrigin, initiatedByFrame frame: WKFrameInfo, type: WKMediaCaptureType, decisionHandler: @escaping @MainActor @Sendable (WKPermissionDecision) -> Void) {
#if DEBUG && targetEnvironment(simulator)
        if VoiceEvaluation.enabled { decisionHandler(.deny); return }
#endif
        guard let expected = self.origin, origin.protocol == "https", origin.host == expected.host,
              (origin.port == 0 ? 443 : origin.port) == (expected.port ?? 443), frame.isMainFrame, type == .microphone else {
            decisionHandler(.deny); return
        }
        decisionHandler(.grant)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) { onMessage?("unavailable", error.localizedDescription) }
    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) { onMessage?("unavailable", error.localizedDescription) }
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) { onMessage?("unavailable", "The audio process ended. Hang up and reopen Voice to reconnect.") }
}

struct VoiceAudioSurface: UIViewRepresentable {
    let bridge: VoiceAudioBridge
    func makeUIView(context: Context) -> WKWebView { bridge.webView }
    func updateUIView(_ uiView: WKWebView, context: Context) {}
}
