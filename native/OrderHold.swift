import Foundation

/// While held, the list keeps the order and rows it showed, and takes fresh
/// data only for rows it still has.
struct OrderHold {
    private(set) var held = false
    private var list: ScopePage.View?
    private var current: ScopePage.View?
    private var rows: [String: SessionState] = [:]
    private var heldCells: [String: SessionCell] = [:]

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

    mutating func cells(live: [String: SessionCell], rows: [String: SessionState]) -> [String: SessionCell] {
        var all = live
        guard held else {
            heldCells = [:]
            return all
        }
        for (id, row) in rows where all[id] == nil {
            let cell = heldCells[id] ?? SessionCell(row)
            heldCells[id] = cell
            all[id] = cell
        }
        return all
    }
}
