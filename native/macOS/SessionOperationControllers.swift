import Combine
import Foundation

private struct OperationAck: Decodable {}

@MainActor
final class NativeQueueController: ObservableObject {
    @Published private(set) var items: [NativeQueuedSubmission] = []
    @Published private(set) var nextCursor: String?
    @Published private(set) var loaded = false
    @Published private(set) var busy = false
    @Published var error: String?
    private(set) var sessionID: String?
    private var clientMessageID = UUID().uuidString
    private var generation: UInt64 = 0

    var canReorder: Bool { loaded && nextCursor == nil && items.count > 1 }

    func reset(sessionID: String) {
        guard sessionID != self.sessionID else { return }
        generation &+= 1
        self.sessionID = sessionID
        items = []
        nextCursor = nil
        loaded = false
        busy = false
        error = nil
        clientMessageID = UUID().uuidString
    }

    func reload(_ api: AgenthailAPI?) async {
        await fetch(api, cursor: nil)
    }

    func loadMore(_ api: AgenthailAPI?) async {
        guard let cursor = nextCursor else { return }
        await fetch(api, cursor: cursor)
    }

    private func fetch(_ api: AgenthailAPI?, cursor: String?) async {
        guard let api, let sessionID, !busy else { return }
        let started = generation
        busy = true
        defer { if generation == started { busy = false } }
        do {
            let page: NativeQueuePage = try await api.nativeQueue(sessionID: sessionID, NativeQueueCommand(queueAction: .list, cursor: cursor))
            guard generation == started else { return }
            if cursor == nil {
                items = page.data
            } else {
                let known = Set(items.map(\.id))
                items += page.data.filter { !known.contains($0.id) }
            }
            nextCursor = page.nextCursor
            loaded = true
            error = nil
        } catch {
            guard generation == started, !error.isCancellation else { return }
            self.error = error.localizedDescription
        }
    }

    func add(_ text: String, api: AgenthailAPI?) async -> Bool {
        let command = NativeQueueCommand(queueAction: .add, message: text, clientUserMessageId: clientMessageID)
        let succeeded = await mutate(api, command)
        if succeeded { clientMessageID = UUID().uuidString }
        return succeeded
    }

    func update(_ item: NativeQueuedSubmission, text: String, api: AgenthailAPI?) async -> Bool {
        await mutate(api, NativeQueueCommand(queueAction: .update, message: text, queuedSubmissionId: item.id))
    }

    func delete(_ item: NativeQueuedSubmission, api: AgenthailAPI?) async {
        await mutate(api, NativeQueueCommand(queueAction: .delete, queuedSubmissionId: item.id))
    }

    func move(_ item: NativeQueuedSubmission, by offset: Int, api: AgenthailAPI?) async {
        guard canReorder, let order = NativeQueueOrder.moving(items.map(\.id), id: item.id, by: offset) else { return }
        await mutate(api, NativeQueueCommand(queueAction: .reorder, queuedSubmissionIds: order))
    }

    func start(_ item: NativeQueuedSubmission?, api: AgenthailAPI?) async -> Bool {
        await mutate(api, NativeQueueCommand(queueAction: .start, queuedSubmissionId: item?.id))
    }

    @discardableResult
    private func mutate(_ api: AgenthailAPI?, _ command: NativeQueueCommand) async -> Bool {
        guard let api, let sessionID, !busy else { return false }
        let started = generation
        busy = true
        do {
            let _: OperationAck = try await api.nativeQueue(sessionID: sessionID, command)
            guard generation == started else { return false }
            busy = false
            error = nil
            await reload(api)
            return true
        } catch {
            guard generation == started else { return false }
            busy = false
            self.error = error.localizedDescription
            return false
        }
    }
}

@MainActor
final class BackgroundSessionController: ObservableObject {
    @Published private(set) var status: SessionLifecycleResult?
    @Published private(set) var busyAction: String?
    @Published private(set) var resumeUnchanged = false
    @Published var logs: String?
    @Published var error: String?
    private(set) var sessionID: String?
    private var generation: UInt64 = 0

    func reset(sessionID: String) {
        guard sessionID != self.sessionID else { return }
        generation &+= 1
        self.sessionID = sessionID
        status = nil
        resumeUnchanged = false
        busyAction = nil
        logs = nil
        error = nil
    }

    func run(_ action: String, api: AgenthailAPI?) async -> Bool {
        guard let api, let sessionID, busyAction == nil else { return false }
        let started = generation
        busyAction = action
        defer { if generation == started { busyAction = nil } }
        do {
            let result = try await api.sessionLifecycle(sessionID: sessionID, action: action)
            guard generation == started else { return false }
            error = nil
            resumeUnchanged = action == "resume" && result.unchanged == true
            switch action {
            case "status": status = result
            case "logs": logs = result.output ?? ""
            default:
                if let refreshed = try? await api.sessionLifecycle(sessionID: sessionID, action: "status"), generation == started { status = refreshed }
            }
            return true
        } catch {
            guard generation == started else { return false }
            self.error = error.localizedDescription
            return false
        }
    }
}

@MainActor
final class ForkController: ObservableObject {
    @Published private(set) var forking = false
    @Published private(set) var stillForking = false
    @Published private(set) var error: String?
    private var pendingKey: (cwd: String?, key: String)?

    func fork(sessionID: String, cwd: String?, api: AgenthailAPI?) async -> ForkedSession? {
        guard !forking else { return nil }
        guard let api else {
            error = "Agenthail isn't connected."
            return nil
        }
        let key = pendingKey.flatMap { $0.cwd == cwd ? $0.key : nil } ?? UUID().uuidString
        pendingKey = nil
        forking = true
        stillForking = false
        error = nil
        defer { forking = false }
        do {
            switch try await api.forkSession(id: sessionID, cwd: cwd, idempotencyKey: key) {
            case .forked(let session):
                return session
            case .submitted:
                pendingKey = (cwd, key)
                stillForking = true
                return nil
            }
        } catch {
            if Self.outcomeUnknown(error) { pendingKey = (cwd, key) }
            self.error = error.localizedDescription
            return nil
        }
    }

    private static func outcomeUnknown(_ error: Error) -> Bool {
        if case AgenthailAPIError.outcomeUnknown = error { return true }
        return error is URLError
    }
}
