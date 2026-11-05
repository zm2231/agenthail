import SwiftUI

/// Holds the list's order while a finger is on it or it is still moving. It
/// only gates when a new order applies and writes no layout during a scroll.
struct OrderHoldTracking: ViewModifier {
    let model: AgenthailIOSModel
    var linger: Duration = .milliseconds(500)
    @State private var touching = false
    @State private var scrolling = false
    @State private var release: Task<Void, Never>?

    func body(content: Content) -> some View {
        content
            .onScrollPhaseChange { _, phase in
                scrolling = phase != .idle
                update()
            }
            .simultaneousGesture(DragGesture(minimumDistance: 0).onChanged { _ in
                guard !touching else { return }
                touching = true
                update()
            }.onEnded { _ in
                touching = false
                update()
            })
    }

    private func update() {
        release?.cancel()
        if touching || scrolling {
            model.holdOrder(true)
            return
        }
        release = Task { @MainActor in
            try? await Task.sleep(for: linger)
            guard !Task.isCancelled else { return }
            model.holdOrder(false)
        }
    }
}

extension View {
    func holdsOrder(_ model: AgenthailIOSModel) -> some View { modifier(OrderHoldTracking(model: model)) }
}
