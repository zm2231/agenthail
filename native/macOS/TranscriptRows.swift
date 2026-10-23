import SwiftUI

/// Which transcript rows a person opened or closed, per session, so switching away and back keeps them.
/// A plain reference rather than an observable object: rows read it when they are built and write it when
/// toggled, so opening one row redraws only that row.
@MainActor
final class TranscriptRowMemory {
    private var rows: [String: [String: Bool]] = [:]

    func isOpen(_ row: String, in session: String) -> Bool? { rows[session]?[row] }

    func set(_ row: String, open: Bool, in session: String) { rows[session, default: [:]][row] = open }
}

/// The transcript's row memory for the session on screen. Equal while the session stays the same.
struct TranscriptRows: Equatable {
    private var memory: TranscriptRowMemory?
    private var session = ""

    init() {}

    init(memory: TranscriptRowMemory, session: String) {
        self.memory = memory
        self.session = session
    }

    @MainActor func isOpen(_ row: String) -> Bool? { memory?.isOpen(row, in: session) }

    @MainActor func set(_ row: String, open: Bool) { memory?.set(row, open: open, in: session) }

    static func == (lhs: Self, rhs: Self) -> Bool { lhs.memory === rhs.memory && lhs.session == rhs.session }
}

extension EnvironmentValues {
    @Entry var transcriptRows = TranscriptRows()
}
