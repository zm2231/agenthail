import AVFAudio
import CallKit
import Foundation

@MainActor
protocol VoiceSystemCalling: AnyObject {
    var onEnd: ((String) -> Void)? { get set }
    var onMute: ((Bool) -> Void)? { get set }
    var onHold: ((Bool) -> Void)? { get set }
    var onActivation: ((AVAudioSession, Bool) -> Void)? { get set }
    func start(id: UUID, name: String) async throws
    func connected()
    func end()
    func mute(_ muted: Bool)
}

@MainActor
final class VoiceSystemCall: NSObject, VoiceSystemCalling, @preconcurrency CXProviderDelegate {
    var onEnd: ((String) -> Void)?
    var onMute: ((Bool) -> Void)?
    var onHold: ((Bool) -> Void)?
    var onActivation: ((AVAudioSession, Bool) -> Void)?
    private let controller = CXCallController()
    private lazy var provider: CXProvider = {
        let configuration = CXProviderConfiguration()
        configuration.supportsVideo = false
        configuration.maximumCallGroups = 1
        configuration.maximumCallsPerCallGroup = 1
        configuration.supportedHandleTypes = [.generic]
        configuration.includesCallsInRecents = false
        let provider = CXProvider(configuration: configuration)
        provider.setDelegate(self, queue: .main)
        return provider
    }()
    private var callID: UUID?
    private var activation: CheckedContinuation<Void, any Error>?
    private var deadline: Task<Void, Never>?

    func start(id: UUID, name: String) async throws {
        guard callID == nil else { throw AgenthailAPIError.unavailable("A system call is already active.") }
        callID = id
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                activation = continuation
                let action = CXStartCallAction(call: id, handle: CXHandle(type: .generic, value: name))
                action.isVideo = false
                controller.request(CXTransaction(action: action)) { [weak self] error in
                    let failure = error?.localizedDescription
                    Task { @MainActor in
                        guard let self, self.callID == id, let failure else { return }
                        self.finishActivation(AgenthailAPIError.unavailable("System call could not start: \(failure)"))
                        self.end()
                    }
                }
                deadline = Task { [weak self] in
                    do { try await Task.sleep(for: .seconds(20)) } catch { return }
                    guard let self, self.callID == id, self.activation != nil else { return }
                    self.finishActivation(AgenthailAPIError.unavailable("iOS did not activate call audio. Call again after any interruption clears."))
                    self.end()
                }
            }
        } onCancel: {
            Task { @MainActor [weak self] in
                guard self?.callID == id else { return }
                self?.end()
            }
        }
    }

    func connected() {
        guard let callID else { return }
        provider.reportOutgoingCall(with: callID, connectedAt: Date())
    }

    func end() {
        guard let id = callID else { return }
        callID = nil
        finishActivation(CancellationError())
        provider.reportCall(with: id, endedAt: Date(), reason: .remoteEnded)
    }

    func mute(_ muted: Bool) {
        guard let callID else { return }
        controller.request(CXTransaction(action: CXSetMutedCallAction(call: callID, muted: muted))) { _ in }
    }

    private func finishActivation(_ error: (any Error)? = nil) {
        deadline?.cancel(); deadline = nil
        let continuation = activation; activation = nil
        if let error { continuation?.resume(throwing: error) }
        else { continuation?.resume() }
    }

    func providerDidReset(_ provider: CXProvider) {
        guard callID != nil else { return }
        callID = nil
        finishActivation(AgenthailAPIError.unavailable("iOS reset the call service. Audio has ended."))
        onEnd?("system_reset")
    }

    func provider(_ provider: CXProvider, perform action: CXStartCallAction) {
        guard action.callUUID == callID else { action.fail(); return }
        do {
            try AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .voiceChat, options: [.allowBluetoothHFP, .defaultToSpeaker])
            provider.reportOutgoingCall(with: action.callUUID, startedConnectingAt: Date())
            action.fulfill()
        } catch {
            action.fail()
            finishActivation(error)
            end()
        }
    }

    func provider(_ provider: CXProvider, perform action: CXEndCallAction) {
        guard action.callUUID == callID else { action.fulfill(); return }
        callID = nil
        finishActivation(CancellationError())
        action.fulfill()
        onEnd?("user_hangup")
    }

    func provider(_ provider: CXProvider, perform action: CXSetMutedCallAction) {
        guard action.callUUID == callID else { action.fail(); return }
        onMute?(action.isMuted)
        action.fulfill()
    }

    func provider(_ provider: CXProvider, perform action: CXSetHeldCallAction) {
        guard action.callUUID == callID else { action.fail(); return }
        onHold?(action.isOnHold)
        action.fulfill()
    }

    func provider(_ provider: CXProvider, didActivate audioSession: AVAudioSession) {
        guard callID != nil else { return }
        onActivation?(audioSession, true)
        finishActivation()
    }

    func provider(_ provider: CXProvider, didDeactivate audioSession: AVAudioSession) {
        onActivation?(audioSession, false)
    }
}
