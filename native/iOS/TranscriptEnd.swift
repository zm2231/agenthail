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
    static let reach: CGFloat = 24

    var atLatest = true
    @ObservationIgnored private var end: TranscriptEnd?
    @ObservationIgnored private var dragStart: (end: TranscriptEnd, following: Bool)?

    func update(_ end: TranscriptEnd) {
        self.end = end
        let reached = end.distance < Self.reach
        if reached != atLatest { atLatest = reached }
    }

    func beginDrag(following: Bool) {
        dragStart = end.map { ($0, following) }
    }

    // Content that grew during the drag moves the end, not the reader; only a reader who scrolled up stops following.
    func endDrag() -> (follow: Bool, catchUp: Bool) {
        defer { dragStart = nil }
        guard let start = dragStart, let end, start.following else { return (atLatest, false) }
        let follow = atLatest || start.end.contentHeight - (end.visibleBottom - end.bottomInset) < Self.reach
        return (follow, follow && !atLatest && end.contentHeight > start.end.contentHeight)
    }
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
                .padding(.bottom, 8)            }
        }
        .task(id: edge.atLatest) {
            stuckBehind = false
            guard !edge.atLatest else { return }
            try? await Task.sleep(for: .seconds(1))
            if !Task.isCancelled, !edge.atLatest { stuckBehind = true }
        }
    }
}
