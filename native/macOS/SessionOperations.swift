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

struct SessionOperationRequest: Encodable {
    let action: String
    let sessionId: String
    var nativeQueue: NativeQueueCommand? = nil

    var mutates: Bool {
        switch action {
        case "native-queue": return nativeQueue?.queueAction != .list
        case "session-lifecycle-status", "session-lifecycle-logs": return false
        default: return true
        }
    }
}

private struct SessionOperationResponse<Result: Decodable>: Decodable {
    let status: String?
    let result: Result?
}

@MainActor
final class ActionRetryKeys {
    private var pending: [Data: String] = [:]
    private let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        return encoder
    }()

    func post<Result: Decodable>(_ request: SessionOperationRequest, api: AgenthailAPI) async throws -> Result {
        guard request.mutates else { return try Self.unwrap(await api.postAction(request)) }
        var request = request
        let adding = request.nativeQueue?.queueAction == .add
        if adding { request.nativeQueue?.clientUserMessageId = nil }
        let identity = try encoder.encode(request)
        let key = pending[identity] ?? UUID().uuidString
        pending[identity] = key
        if adding { request.nativeQueue?.clientUserMessageId = key }
        do {
            let result: Result = try Self.unwrap(await api.postAction(request, idempotencyKey: key))
            pending[identity] = nil
            return result
        } catch {
            let uncertain = Self.uncertain(error)
            if uncertain == nil { pending[identity] = nil }
            throw uncertain ?? error
        }
    }

    private static func unwrap<Result>(_ response: SessionOperationResponse<Result>) throws -> Result {
        if let result = response.result { return result }
        guard response.status == "submitted" else { throw AgenthailAPIError.invalidResponse }
        throw AgenthailAPIError.outcomeUnknown("Agenthail is still finishing this request.")
    }

    private static func uncertain(_ error: Error) -> AgenthailAPIError? {
        if case AgenthailAPIError.outcomeUnknown = error { return error as? AgenthailAPIError }
        guard let error = error as? URLError else { return nil }
        let notSent: Set<URLError.Code> = [.cannotConnectToHost, .cannotFindHost, .dnsLookupFailed, .notConnectedToInternet, .badURL, .unsupportedURL, .userAuthenticationRequired]
        return notSent.contains(error.code) ? nil : .outcomeUnknown(error.localizedDescription)
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

enum SessionOperationAvailability {
    static func fork(_ session: SessionState) -> Bool { session.surface == "codex" && !session.isReadOnly }
    static func nativeQueue(_ session: SessionState) -> Bool { session.surface == "codex" }
    static func nativeQueueWritable(_ session: SessionState) -> Bool { session.surface == "codex" && !session.isReadOnly }
    static func lifecycle(_ session: SessionState) -> Bool { session.surface == "claude" && !session.isReadOnly }
}

extension AgenthailAPI {
    @MainActor
    func forkSession(id: String, keys: ActionRetryKeys) async throws -> ForkedSession {
        let result: SessionForkResult = try await keys.post(SessionOperationRequest(action: "session-fork", sessionId: id), api: self)
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
