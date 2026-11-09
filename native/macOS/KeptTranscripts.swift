import AppKit

@MainActor
final class KeptTranscripts {
    static let limit = 4

    struct Views {
        let make: (SessionPane, SessionState) -> NSView
        let bind: (NSView, SessionPane, SessionState) -> Void
    }

    private struct Entry {
        let id: String
        let host: NSView
        var session: SessionState
        var frozen: SessionPane?
    }

    private var entries: [Entry] = []
    private var views: Views?
    private var pressure: DispatchSourceMemoryPressure?

    var keptIDs: [String] { entries.map(\.id) }

    func host(for id: String) -> NSView? { entries.first { $0.id == id }?.host }

    func isFrozen(_ id: String) -> Bool { entries.first { $0.id == id }?.frozen != nil }

    func show(_ session: SessionState, live pane: SessionPane, in container: NSView, views: Views) {
        self.views = views
        watchMemoryPressure()
        var entry: Entry
        if let index = entries.firstIndex(where: { $0.id == session.id }) {
            entry = entries.remove(at: index)
            entry.session = session
            entry.frozen = nil
            views.bind(entry.host, pane, session)
        } else {
            entry = Entry(id: session.id, host: views.make(pane, session), session: session)
        }
        entries.insert(entry, at: 0)
        if entry.host.superview !== container {
            container.subviews.forEach { $0.removeFromSuperview() }
            entry.host.frame = container.bounds
            container.addSubview(entry.host)
        }
        trim(to: Self.limit)
    }

    func freeze(_ pane: SessionPane) {
        guard let views, let id = pane.displayedSession?.id ?? pane.selectedSessionID,
              let index = entries.firstIndex(where: { $0.id == id }), entries[index].frozen == nil else { return }
        let frozen = pane.frozenTranscript()
        entries[index].frozen = frozen
        // Out of the window first: a detached host starts no tasks, so nothing frozen loads.
        entries[index].host.removeFromSuperview()
        views.bind(entries[index].host, frozen, entries[index].session)
    }

    func relieveMemoryPressure() {
        trim(to: 1)
    }

    func removeAll() {
        trim(to: 0)
        pressure?.cancel()
        pressure = nil
    }

    private func trim(to count: Int) {
        while entries.count > count { entries.removeLast().host.removeFromSuperview() }
    }

    private func watchMemoryPressure() {
        guard pressure == nil else { return }
        let source = DispatchSource.makeMemoryPressureSource(eventMask: [.warning, .critical], queue: .main)
        source.setEventHandler { [weak self] in MainActor.assumeIsolated { self?.relieveMemoryPressure() } }
        source.activate()
        pressure = source
    }

    isolated deinit {
        pressure?.cancel()
    }
}
