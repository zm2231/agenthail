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

struct ForkCommand: Encodable, Equatable {
    var cwd: String? = nil
}

struct SessionOperationRequest: Encodable {
    let action: String
    let sessionId: String
    var fork: ForkCommand? = nil
    var nativeQueue: NativeQueueCommand? = nil
}

struct SessionOperationResponse<Result: Decodable>: Decodable {
    let result: Result
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
    func forkSession(id: String, cwd: String?, idempotencyKey: String? = nil) async throws -> SessionForkResult {
        let request = SessionOperationRequest(action: "session-fork", sessionId: id, fork: ForkCommand(cwd: cwd))
        let response: SessionOperationResponse<SessionForkResult> = try await postAction(request, idempotencyKey: idempotencyKey)
        return response.result
    }

    func nativeQueue<Result: Decodable>(sessionID: String, _ command: NativeQueueCommand, idempotencyKey: String? = nil) async throws -> Result {
        let request = SessionOperationRequest(action: "native-queue", sessionId: sessionID, nativeQueue: command)
        let response: SessionOperationResponse<Result> = try await postAction(request, idempotencyKey: idempotencyKey)
        return response.result
    }

    func sessionLifecycle(sessionID: String, action: String) async throws -> SessionLifecycleResult {
        let request = SessionOperationRequest(action: "session-lifecycle-" + action, sessionId: sessionID)
        let response: SessionOperationResponse<SessionLifecycleResult> = try await postAction(request)
        return response.result
    }
}
