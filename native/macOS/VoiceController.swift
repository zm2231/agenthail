import AppKit

@MainActor
final class VoiceController: ObservableObject {
    @Published private(set) var call: VoiceOperatorModel?
    private let makeCall: @MainActor (String) -> VoiceOperatorModel

    init(makeCall: @escaping @MainActor (String) -> VoiceOperatorModel = VoiceController.localCall) {
        self.makeCall = makeCall
    }

    func route(to targetID: String) {
        if let call {
            call.targetID = targetID
            return
        }
        let call = makeCall(targetID)
        self.call = call
        call.open()
    }

    func close() {
        call?.close()
        call = nil
    }

    static func localCall(targetID: String) -> VoiceOperatorModel {
        do {
            let local = try AgenthailAPI.localDaemon()
            return VoiceOperatorModel(endpoint: loopback(local.baseURL), token: local.token, targetID: targetID)
        } catch {
            return VoiceOperatorModel(targetID: targetID, unavailable: error.localizedDescription)
        }
    }

    static func loopback(_ url: URL) -> URL {
        guard var components = URLComponents(url: url, resolvingAgainstBaseURL: false),
              ["0.0.0.0", "::", ""].contains(components.host ?? "") else { return url }
        components.host = "127.0.0.1"
        return components.url ?? url
    }
}

enum VoicePrivacySettings {
    static let microphoneURL = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone")!

    static func openMicrophone() { NSWorkspace.shared.open(microphoneURL) }
}
