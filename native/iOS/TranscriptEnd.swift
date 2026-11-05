import SwiftUI

struct TranscriptEnd: Equatable {
    let contentHeight: CGFloat
    let visibleBottom: CGFloat
    let bottomInset: CGFloat

    init(_ geometry: ScrollGeometry) {
        contentHeight = geometry.contentSize.height
        visibleBottom = geometry.visibleRect.maxY
        bottomInset = geometry.contentInsets.bottom
    }

    var distance: CGFloat { contentHeight - (visibleBottom - bottomInset) }
}

@MainActor @Observable
final class TranscriptEdge {
    var atLatest = true
}

struct JumpToLatestControl: View {
    let edge: TranscriptEdge
    let following: Bool
    let action: () -> Void
    @State private var stuckBehind = false

    var body: some View {
        ZStack {
            if !edge.atLatest && (!following || stuckBehind) {
                Button(action: action) {
                    Image(systemName: "arrow.down")
                        .font(.body.weight(.semibold))
                        .frame(width: 44, height: 44)
                        .floatingGlass(in: Circle(), interactive: true)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Jump to latest")
                .padding(.bottom, 8)
            }
        }
        .task(id: edge.atLatest) {
            stuckBehind = false
            guard !edge.atLatest else { return }
            try? await Task.sleep(for: .seconds(1))
            if !Task.isCancelled, !edge.atLatest { stuckBehind = true }
        }
    }
}
