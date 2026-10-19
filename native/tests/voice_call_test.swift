import Foundation

@main
struct VoiceCallTest {
    @MainActor
    static func main() async {
        let service = FixtureVoiceService()
        let audio = FixtureAudio()
        let controller = VoiceController { VoiceOperatorModel(targetID: $0, api: service, audio: audio) }

        controller.route(to: "")
        guard let call = controller.call else { return fail("routing opens a call") }
        check(audio.loaded == ["https://mac.test/api/v1/voice/peer"], "opening loads the authenticated peer page")
        audio.emit("ready")
        await settle()
        check(call.connectionTitle == "Ready to talk" && call.canCall, "idle call is ready to talk")

        controller.route(to: "fixture-session")
        check(controller.call === call && call.targetID == "fixture-session", "calling a session reuses the open call and prefills the route")
        check(service.actions.isEmpty, "prefilling a route sends nothing to the host")

        await call.call()
        check(service.names == ["select", "prepare"] && service.actions[0].targetId == "fixture-session", "a call selects the route then prepares")
        check(audio.starts == 1 && call.dialing && call.connectionTitle == "Connecting audio…", "a call starts the microphone and shows connecting")

        service.phase = "negotiating"
        audio.emit("offer", "v=0 fixture-offer")
        await settle()
        let start = service.actions.last
        check(start?.action == "start" && start?.sdp == "v=0 fixture-offer" && start?.attemptId != nil, "the offer starts the host call with an attempt id")
        audio.emit("connection", "connected")
        audio.emit("channel", "open")
        await settle()
        check(service.names.last == "connected" && service.actions.last?.attemptId == start?.attemptId, "connected is reported for the same attempt")
        service.phase = "connected"
        await call.refresh()
        check(call.audioConnected && !call.dialing && call.connectionTitle == "Connected", "the call shows connected")

        call.targetID = "builder"
        service.target = "builder"
        await call.transfer()
        check(service.actions.last?.action == "transfer" && service.actions.last?.targetId == "builder", "transfer routes to the named session")
        check(call.state?.target?.id == "builder", "the transferred route is shown")
        await call.interruptTarget()
        check(service.actions.last?.action == "target-interrupt" && service.actions.last?.attemptId == start?.attemptId, "stop selected session interrupts the routed turn of this call")
        call.targetID = ""
        service.target = nil
        await call.transfer()
        check(service.actions.last?.action == "transfer" && service.actions.last?.targetId == "", "an empty route returns to the orchestrator")

        call.hangup()
        check(audio.ends > 0 && !call.audioConnected, "hang up ends local audio at once")
        await settle()
        check(service.actions.last?.action == "stop" && service.actions.last?.attemptId == start?.attemptId, "hang up stops the host call")

        service.phase = "ended"
        await call.refresh()
        audio.denyMicrophone = true
        await call.call()
        check(call.microphoneDenied && !call.dialing, "a refused microphone is a visible mic-denied state")
        check(call.error == VoiceMicrophoneDenied().localizedDescription && call.error?.contains("System Settings") == true, "mic denied explains where to turn it on")
        check(call.canCall, "the call can be retried after granting access")
        audio.denyMicrophone = false
        await call.call()
        check(!call.microphoneDenied && call.dialing, "a new attempt clears the mic-denied state")
        audio.emit("error", "Codex voice data channel failed")
        await settle()
        check(call.error == "Codex voice data channel failed" && !call.dialing, "an audio error is shown and ends the attempt")

        let closes = audio.closes
        controller.close()
        check(controller.call == nil && audio.closes == closes + 1, "closing the window closes the call")
        controller.route(to: "")
        check(controller.call !== call, "reopening starts a fresh call")

        check(VoiceController.loopback(URL(string: "http://0.0.0.0:7412")!).absoluteString == "http://127.0.0.1:7412", "a wildcard listener is reached over loopback")
        check(VoiceController.loopback(URL(string: "http://127.0.0.1:7412")!).absoluteString == "http://127.0.0.1:7412", "a loopback listener is unchanged")
        check(VoicePrivacySettings.microphoneURL.absoluteString.hasSuffix("Privacy_Microphone"), "Open System Settings targets the microphone pane")
        print("voice call ok")
    }

    @MainActor
    private static func settle() async {
        for _ in 0..<20 { await Task.yield() }
    }

    private static func fail(_ message: String) {
        check(false, message)
    }

    private static func check(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}

@MainActor
private final class FixtureVoiceService: VoiceServiceClient {
    var actions: [VoiceAction] = []
    var phase = "ready"
    var target: String?
    var names: [String] { actions.map(\.action) }

    func request(path: String) -> URLRequest { URLRequest(url: URL(string: "https://mac.test/\(path)")!) }
    func state() async throws -> VoiceState { snapshot() }
    func action(_ action: VoiceAction) async throws -> VoiceState {
        actions.append(action)
        return snapshot(attempt: action.attemptId)
    }

    private func snapshot(attempt: String? = nil) -> VoiceState {
        let routed = target.map { #","target":{"id":"\#($0)","name":"\#($0)","surface":"claude","status":"busy","lastActive":"2026-01-01T12:00:00Z"}"# } ?? ""
        let attemptID = actions.last(where: { $0.action == "start" })?.attemptId ?? attempt
        let attemptField = attemptID.map { #","attemptId":"\#($0)""# } ?? ""
        let json = #"{"protocol":1,"phase":"\#(phase)","events":[],"truncated":false,"occupied":false\#(attemptField)\#(routed)}"#
        return try! JSONDecoder().decode(VoiceState.self, from: Data(json.utf8))
    }
}

@MainActor
private final class FixtureAudio: VoiceAudioClient {
    var onMessage: ((String, String) -> Void)?
    var loaded: [String] = []
    var starts = 0
    var ends = 0
    var closes = 0
    var denyMicrophone = false

    func load(_ request: URLRequest) { loaded.append(request.url?.absoluteString ?? "") }
    func start() async throws {
        starts += 1
        if denyMicrophone { throw VoiceMicrophoneDenied() }
    }
    func end() { ends += 1 }
    func answer(_ sdp: String) async throws {}
    func mute(_ muted: Bool) {}
    func close() { closes += 1 }
    func emit(_ type: String, _ value: String = "") { onMessage?(type, value) }
}
