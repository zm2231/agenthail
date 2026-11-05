import AppKit
import SwiftUI

/// Holds the sidebar's order while the pointer moves over it or a button is
/// down; a pointer parked longer than `settle` lets news through.
struct OrderHoldTracking: ViewModifier {
    let model: AgenthailModel
    var settle: Duration = .seconds(3)
    @State private var release: Task<Void, Never>?

    func body(content: Content) -> some View {
        content.onContinuousHover { phase in
            switch phase {
            case .active:
                model.holdOrder(true)
                scheduleRelease()
            case .ended:
                releaseWhenUp()
            }
        }
        .onDisappear {
            release?.cancel()
            model.holdOrder(false)
        }
    }

    private func scheduleRelease() {
        release?.cancel()
        release = Task { @MainActor in
            try? await Task.sleep(for: settle)
            guard !Task.isCancelled else { return }
            await waitForButtonsUp()
            guard !Task.isCancelled else { return }
            model.holdOrder(false)
        }
    }

    private func releaseWhenUp() {
        release?.cancel()
        release = Task { @MainActor in
            await waitForButtonsUp()
            guard !Task.isCancelled else { return }
            model.holdOrder(false)
        }
    }

    private func waitForButtonsUp() async {
        while NSEvent.pressedMouseButtons != 0, !Task.isCancelled {
            try? await Task.sleep(for: .milliseconds(100))
        }
    }
}

extension View {
    func holdsOrder(_ model: AgenthailModel) -> some View { modifier(OrderHoldTracking(model: model)) }
}
