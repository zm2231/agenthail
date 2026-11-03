import Foundation

@MainActor
enum HostRefresh {
    static let wait: Duration = .seconds(6)

    // A slow computer is not waited for; its refresh still lands when it answers.
    static func all(_ hosts: [HostConnection], fresh: Bool, within wait: Duration = wait) async -> Bool {
        await withTaskGroup(of: Bool.self) { group in
            for host in hosts {
                group.addTask { await answer(host, fresh: fresh, within: wait) }
            }
            var any = false
            for await answered in group where answered { any = true }
            return any
        }
    }

    private static func answer(_ host: HostConnection, fresh: Bool, within wait: Duration) async -> Bool {
        await withCheckedContinuation { continuation in
            let first = FirstAnswer(continuation)
            Task { @MainActor in first.resume(await host.refresh(fresh: fresh)) }
            Task {
                try? await Task.sleep(for: wait)
                first.resume(false)
            }
        }
    }
}

private final class FirstAnswer: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Bool, Never>?

    init(_ continuation: CheckedContinuation<Bool, Never>) { self.continuation = continuation }

    func resume(_ value: Bool) {
        let waiting = lock.withLock { () -> CheckedContinuation<Bool, Never>? in
            defer { continuation = nil }
            return continuation
        }
        waiting?.resume(returning: value)
    }
}
