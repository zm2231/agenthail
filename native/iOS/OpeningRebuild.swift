import SwiftUI

// LazyVStack leaves rows laid out by the opening scroll out of accessibility until they are rebuilt.
@MainActor
final class OpeningRebuild {
    private var armed = false
    private var landed = false
    private var appeared: String?

    func open(following: Bool) {
        armed = following
        landed = false
    }

    func rowAppeared(_ id: String) { appeared = id }

    func land() { landed = true }

    func fire(latest: String?, _ rebuild: @escaping @MainActor () -> Void) {
        guard armed, landed, let latest, appeared == latest else { return }
        armed = false
        Task { @MainActor in rebuild() }
    }
}

extension View {
    // Unique per row: lazy stack rows sharing one inner id collapse into a single row.
    func rebuilt(_ id: String, generation: Int) -> some View {
        self.id("\(id)#\(generation)")
    }
}
