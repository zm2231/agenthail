import Foundation

/// While held, the list keeps the order and rows it showed, and takes fresh
/// data only for rows it still has.
struct OrderHold {
    private(set) var held = false
    private var list: ScopePage.View?
    private var current: ScopePage.View?
    private var rows: [String: SessionState] = [:]

    mutating func hold(list: ScopePage.View?, current: ScopePage.View?, rows: [String: SessionState]) {
        guard !held else { return }
        (held, self.list, self.current, self.rows) = (true, list, current, rows)
    }

    mutating func release() {
        (held, list, current, rows) = (false, nil, nil, [:])
    }

    func shown(list: ScopePage.View?, current: ScopePage.View?, rows fresh: [String: SessionState]) -> (list: ScopePage.View?, current: ScopePage.View?, rows: [String: SessionState]) {
        guard held else { return (list, current, fresh) }
        return (self.list, self.current, rows.merging(fresh) { _, new in new })
    }
}
