// Equal by presence only, so a fresh closure each render doesn't redraw the row.
struct TranscriptRowAction<Input>: Equatable {
    let closure: ((Input) -> Void)?

    init(_ closure: ((Input) -> Void)?) {
        self.closure = closure
    }

    static func == (lhs: Self, rhs: Self) -> Bool { (lhs.closure == nil) == (rhs.closure == nil) }
}
