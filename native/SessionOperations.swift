import Foundation

struct NativeQueueCommand: Encodable, Equatable {
    enum Action: String, Encodable { case list, add, update, delete, reorder, start }

    let queueAction: Action
    var message: String? = nil
    var queuedSubmissionId: String? = nil
    var clientUserMessageId: String? = nil
    var queuedSubmissionIds: [String]? = nil
    var cursor: String? = nil
}

struct ForkPoint: Encodable, Hashable {
    let beforeTurnId: String
}

struct SessionOperationRequest: Encodable {
    let action: String
    let sessionId: String
    var nativeQueue: NativeQueueCommand? = nil
    var fork: ForkPoint? = nil

    var mutates: Bool {
        switch action {
        case "native-queue": return nativeQueue?.queueAction != .list
        case "session-lifecycle-status", "session-lifecycle-logs": return false
        default: return true
        }
    }

    var observesState: Bool { nativeQueue?.queueAction == .list || action == "session-lifecycle-status" }

    var scope: ActionScope {
        ActionScope(sessionID: sessionId, family: action.hasPrefix("session-lifecycle-") ? "session-lifecycle" : action)
    }
}

struct ActionScope: Hashable {
    let sessionID: String
    let family: String

    static func fork(_ sessionID: String) -> ActionScope { ActionScope(sessionID: sessionID, family: "session-fork") }
    static let creation = ActionScope(sessionID: "", family: "session-create")
}

private struct SessionOperationResponse<Result: Decodable>: Decodable {
    let status: String?
    let result: Result?
}

@MainActor
final class ActionRetryKeys {
    private struct Pending {
        let key: String
        let scope: ActionScope
        var recorded = false
    }

    private var pending: [Data: Pending] = [:]
    private let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        return encoder
    }()
    static let notSent: Set<URLError.Code> = [.cannotConnectToHost, .cannotFindHost, .dnsLookupFailed, .notConnectedToInternet, .badURL, .unsupportedURL, .userAuthenticationRequired]

    func post<Result: Decodable>(_ request: SessionOperationRequest, api: AgenthailAPI) async throws -> Result {
        guard request.mutates else {
            let response: SessionOperationResponse<Result> = try await api.postAction(request)
            guard let result = response.result else { throw AgenthailAPIError.invalidResponse }
            if request.observesState { pending = pending.filter { $0.value.scope != request.scope || !$0.value.recorded } }
            return result
        }
        var request = request
        let adding = request.nativeQueue?.queueAction == .add
        if adding { request.nativeQueue?.clientUserMessageId = nil }
        let identity = try encoder.encode(request)
        let key = key(for: identity, scope: request.scope)
        if adding { request.nativeQueue?.clientUserMessageId = key }
        let response: SessionOperationResponse<Result>
        do {
            response = try await api.postAction(request, idempotencyKey: key)
        } catch AgenthailAPIError.outcomeUnknown(let message) {
            pending[identity]?.recorded = true
            throw AgenthailAPIError.outcomeUnknown(message)
        } catch let error as URLError where !Self.notSent.contains(error.code) {
            throw AgenthailAPIError.outcomeUnknown(AgenthailAPIError.userMessage(error))
        } catch {
            abandon(request.scope)
            throw error
        }
        if let result = response.result {
            abandon(request.scope)
            return result
        }
        guard response.status == "submitted" else {
            abandon(request.scope)
            throw AgenthailAPIError.invalidResponse
        }
        throw AgenthailAPIError.outcomeUnknown("Agenthail is still finishing this request.")
    }

    func key(for identity: Data, scope: ActionScope) -> String {
        let key = pending[identity]?.key ?? UUID().uuidString
        pending[identity] = Pending(key: key, scope: scope)
        return key
    }

    func abandon(_ scope: ActionScope) {
        pending = pending.filter { $0.value.scope != scope }
    }
}

struct NativeQueueInput: Decodable, Equatable {
    let type: String
    let text: String?
    let name: String?
    let path: String?
    let url: String?

    var summary: String {
        switch type {
        case "text": return text ?? ""
        case "image", "localImage": return "Image"
        case "audio", "localAudio": return "Audio"
        default: return name ?? type
        }
    }
}

struct NativeQueuedSubmission: Decodable, Identifiable, Equatable {
    let id: String
    let clientUserMessageId: String?
    let input: [NativeQueueInput]

    var text: String { input.map(\.summary).filter { !$0.isEmpty }.joined(separator: "\n") }
    var editableText: String? {
        guard input.allSatisfy({ $0.type == "text" }) else { return nil }
        return input.compactMap(\.text).joined(separator: "\n")
    }
}

struct NativeQueuePage: Decodable {
    let data: [NativeQueuedSubmission]
    let nextCursor: String?
}

struct ForkedSession: Decodable {
    let id: String
}

struct SessionForkResult: Decodable {
    let session: ForkedSession
}

struct SessionLifecycleResult: Decodable, Equatable {
    let id: String?
    let sessionId: String?
    let state: String?
    let action: String?
    let output: String?
    let unchanged: Bool?
}

enum NativeQueueOrder {
    static func moving(_ ids: [String], id: String, by offset: Int) -> [String]? {
        guard let index = ids.firstIndex(of: id) else { return nil }
        let target = index + offset
        guard ids.indices.contains(target), target != index else { return nil }
        var next = ids
        next.swapAt(index, target)
        return next
    }
}

enum SessionLifecycleCopy {
    static func title(_ session: SessionState) -> String {
        session.runtimeKind == "claude-bg" ? "Background session" : "Session"
    }

    static func detail(_ session: SessionState) -> String {
        switch session.runtimeKind {
        case "claude-bg": return "A Claude Code background job."
        case "claude-host": return "Agenthail runs this session. Logs shows what its Claude process wrote to stderr; Stop closes it, and the next message or Resume opens it again."
        default: return "Controls for this session's process."
        }
    }
}

enum SessionOperationAvailability {
    static func fork(_ session: SessionState) -> Bool { session.surface == "codex" && !session.isReadOnly }
    static func nativeQueue(_ session: SessionState) -> Bool { session.surface == "codex" }
    static func nativeQueueWritable(_ session: SessionState) -> Bool { session.surface == "codex" && !session.isReadOnly }
    static func lifecycle(_ session: SessionState) -> SessionLifecycle { session.isReadOnly ? SessionLifecycle() : session.capabilities.lifecycle }
}

extension AgenthailAPI {
    @MainActor
    func forkSession(id: String, before turnID: String? = nil, keys: ActionRetryKeys) async throws -> ForkedSession {
        let result: SessionForkResult = try await keys.post(SessionOperationRequest(action: "session-fork", sessionId: id, fork: turnID.map(ForkPoint.init)), api: self)
        return result.session
    }

    @MainActor
    func nativeQueue<Result: Decodable>(sessionID: String, _ command: NativeQueueCommand, keys: ActionRetryKeys) async throws -> Result {
        try await keys.post(SessionOperationRequest(action: "native-queue", sessionId: sessionID, nativeQueue: command), api: self)
    }

    @MainActor
    func sessionLifecycle(sessionID: String, action: String, keys: ActionRetryKeys) async throws -> SessionLifecycleResult {
        try await keys.post(SessionOperationRequest(action: "session-lifecycle-" + action, sessionId: sessionID), api: self)
    }
}
