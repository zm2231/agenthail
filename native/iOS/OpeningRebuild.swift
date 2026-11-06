import SwiftUI

// LazyVStack leaves rows laid out by the opening scroll out of accessibility until they are rebuilt.
@MainActor
final class OpeningRebuild {
    private var armed = false
    private var landed = false
    private var appeared: String?
    private var current = 0

    func open(following: Bool) -> Int {
        current += 1
        armed = following
        landed = false
        appeared = nil
        return current
    }

    func rowAppeared(_ id: String) { appeared = id }

    func land(_ open: Int) {
        if open == current { landed = true }
    }

    func fire(latest: String?, _ rebuild: @escaping @MainActor () -> Void) {
        guard armed, landed, let latest, appeared == latest else { return }
        armed = false
        let open = current
        Task { @MainActor [weak self] in
            if self?.current == open { rebuild() }
        }
    }
}

extension View {
    // Unique per row: lazy stack rows sharing one inner id collapse into a single row.
    func rebuilt(_ id: String, generation: Int) -> some View {
        self.id("\(id)#\(generation)")
    }
}
