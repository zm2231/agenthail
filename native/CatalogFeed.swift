import Foundation

@MainActor
final class CatalogFeed {
    struct Host {
        let api: () -> AgenthailAPI?
        let cursor: () -> UInt64
        let refresh: () async -> Bool
        let resetCursor: () -> Void
        let apply: ([CatalogStreamEvent]) -> Void
        var stopped: () -> Bool = { false }
    }

    var bound = 2000
    private let host: Host
    private var task: Task<Void, Never>?
    private var inbox: CatalogInbox?

    init(_ host: Host) {
        self.host = host
    }

    deinit {
        task?.cancel()
    }

    var isRunning: Bool { task != nil }

    // After a gap or an overflow the stream reopens only once a fresh snapshot is adopted.
    func start(afterFreshSnapshot: Bool = false) {
        task?.cancel()
        inbox = nil
        let host = host
        task = Task { [weak self] in
            let backoff = EventRetryBackoff()
            var resync = afterFreshSnapshot
            while !Task.isCancelled, !host.stopped() {
                if resync {
                    let adopted = await host.refresh()
                    if Task.isCancelled || host.stopped() { return }
                    guard adopted else {
                        try? await Task.sleep(for: .seconds(backoff.nextDelay()))
                        continue
                    }
                    resync = false
                }
                guard let api = host.api(), let reading = self?.openInbox() else { return }
                do {
                    try await api.streamCatalog(after: host.cursor(), onConnected: {}, onEvent: { event in
                        backoff.reset()
                        reading.append(event)
                    })
                } catch {
                    if Task.isCancelled || host.stopped() { return }
                    if case AgenthailAPIError.streamGap = error {
                        self?.inbox = nil
                        host.resetCursor()
                        resync = true
                        continue
                    }
                    try? await Task.sleep(for: .seconds(backoff.nextDelay()))
                }
            }
        }
    }

    func stop() {
        task?.cancel()
        task = nil
        inbox = nil
    }

    func openInbox() -> CatalogInbox {
        let opened = CatalogInbox(bound: bound) { [weak self] in
            RunLoop.main.perform(inModes: [.common]) {
                MainActor.assumeIsolated { self?.drain() }
            }
        }
        inbox = opened
        return opened
    }

    private func drain() {
        guard let inbox, !host.stopped() else { return }
        switch inbox.take() {
        case .overflow: start(afterFreshSnapshot: true)
        case .events(let events): host.apply(events)
        }
    }
}
