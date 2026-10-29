import SwiftUI

/// Which transcript rows a person opened or closed, per session, so switching away and back keeps them.
/// A plain reference rather than an observable object: rows read it when they are built and write it when
/// toggled, so opening one row redraws only that row.
@MainActor
final class TranscriptRowMemory {
    private var rows: [String: [String: Bool]] = [:]
    private var counts: [String: [String: Int]] = [:]
    private var bodies: [String: [String: RetainedBodyResult]] = [:]

    func isOpen(_ row: String, in session: String) -> Bool? { rows[session]?[row] }

    func body(_ row: String, in session: String) -> RetainedBodyResult? { bodies[session]?[row] }

    func set(_ row: String, body: RetainedBodyResult, in session: String) { bodies[session, default: [:]][row] = body }

    func set(_ row: String, open: Bool, in session: String) { rows[session, default: [:]][row] = open }

    func count(_ row: String, in session: String) -> Int? { counts[session]?[row] }

    func set(_ row: String, count: Int, in session: String) { counts[session, default: [:]][row] = count }
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

    /// How many lines of a row's output are shown, such as the pages of a long log.
    @MainActor func count(_ row: String) -> Int? { memory?.count(row, in: session) }

    @MainActor func set(_ row: String, count: Int) { memory?.set(row, count: count, in: session) }

    @MainActor func body(_ row: String) -> RetainedBodyResult? { memory?.body(row, in: session) }

    @MainActor func set(_ row: String, body: RetainedBodyResult) { memory?.set(row, body: body, in: session) }

    /// A row's own copy of a choice, so toggling redraws that row. It counts only while the transcript shows
    /// the session it was made in: rows outlive a session switch and timeline ids can repeat across sessions.
    func choice<Value>(_ value: Value) -> RowChoice<Value> { RowChoice(session: session, value: value) }

    func current<Value>(_ choice: RowChoice<Value>?) -> Value? {
        guard let choice, choice.session == session else { return nil }
        return choice.value
    }

    static func == (lhs: Self, rhs: Self) -> Bool { lhs.memory === rhs.memory && lhs.session == rhs.session }
}

// Rows may keep an older equal copy: `perform` must reach live state (@State, a binding, a reference), never captured values.
struct TranscriptAction<Input>: Equatable {
    private let owner: String
    private let perform: @MainActor (Input) -> Void

    init(owner: String, perform: @escaping @MainActor (Input) -> Void) {
        self.owner = owner
        self.perform = perform
    }

    @MainActor func callAsFunction(_ input: Input) { perform(input) }

    static func == (lhs: Self, rhs: Self) -> Bool { lhs.owner == rhs.owner }
}

struct RowChoice<Value> {
    fileprivate let session: String
    fileprivate let value: Value
}

extension EnvironmentValues {
    @Entry var transcriptRows = TranscriptRows()
    /// What became of each call on screen that has no result.
    @Entry var toolCallStates = ToolCallStates()
    /// Scrolls the transcript so this point of its content, measured in `TranscriptRows.contentSpace`, sits
    /// at the top. Set by the view that owns the scroll view. A collapse from the end calls it in the same
    /// transaction: it scrolls to an offset rather than to a row id, because once the offset is past the
    /// shrunken content no row is laid out and an id lands on the stack's estimated heights.
    @Entry var revealRow = TranscriptAction<CGFloat>(owner: "") { _ in }
}

extension TranscriptRows {
    /// The coordinate space of a transcript's scroll content.
    static let contentSpace = "transcript-content"
}

extension View {
    /// Keeps `top` at this view's top in the transcript's content, for `revealRow`.
    func contentTop(_ top: Binding<CGFloat>) -> some View {
        onGeometryChange(for: CGFloat.self) { $0.frame(in: .named(TranscriptRows.contentSpace)).minY } action: { top.wrappedValue = $0 }
    }
}
