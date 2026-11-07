func sessionLoadIsCurrent(_ requestedID: String, selectedID: String?) -> Bool {
    requestedID == selectedID
}

/// Session ids the daemon retired, each mapped to the id its session
/// continues under. A client resolves every id it still holds through it:
/// selections, drafts, restored windows and notification targets.
struct SessionRekeys {
    private var map: [String: String] = [:]

    var isEmpty: Bool { map.isEmpty }

    mutating func record(_ rekeys: [String: String]) {
        map.merge(rekeys) { _, new in new }
    }

    func resolve(_ id: String) -> String {
        var current = id
        var seen: Set<String> = [id]
        while let next = map[current], seen.insert(next).inserted { current = next }
        return current
    }

    /// Moves values held under retired ids to the ids they continue under,
    /// keeping a value already held under the new id. Reports whether
    /// anything moved.
    func carry<Value>(_ values: inout [String: Value]) -> Bool {
        var moved = false
        for old in map.keys {
            let new = resolve(old)
            guard new != old, let value = values.removeValue(forKey: old) else { continue }
            if values[new] == nil { values[new] = value }
            moved = true
        }
        return moved
    }
}
