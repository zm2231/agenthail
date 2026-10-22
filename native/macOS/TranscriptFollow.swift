import SwiftUI

struct TranscriptFollow<ID: Equatable>: ViewModifier {
    let transcript: ID
    let leadingRow: String?
    @Binding var position: ScrollPosition
    @Binding var following: Bool
    @State private var phase: ScrollPhase = .idle
    @State private var generation = 0
    @State private var placedLeadingRow: String?

    static var bottomSlack: CGFloat { 70 }

    static func atBottom(_ geometry: ScrollGeometry) -> Bool {
        geometry.contentOffset.y + geometry.containerSize.height >= geometry.contentSize.height - bottomSlack
    }

    func body(content: Content) -> some View {
        content
            .scrollPosition($position)
            .defaultScrollAnchor(.bottom, for: .initialOffset)
            .defaultScrollAnchor(following ? .bottom : nil, for: .sizeChanges)
            .onScrollPhaseChange { _, new in phase = new }
            .onScrollGeometryChange(for: ScrollGeometry.self) { $0 } action: { old, new in
                let inserted = leadingRow != placedLeadingRow
                placedLeadingRow = leadingRow
                if old.contentSize == new.contentSize && old.containerSize == new.containerSize {
                    // A jump's animation passes through offsets short of the bottom; they are not the reader leaving it.
                    if phase != .animating { following = Self.atBottom(new) }
                } else if following, phase == .idle || phase == .animating, !Self.atBottom(new) {
                    settle { $0.scrollTo(edge: .bottom) }
                } else if !following, inserted, phase == .idle {
                    let target = max(new.contentSize.height - (old.contentSize.height - old.contentOffset.y), 0)
                    settle { $0.scrollTo(y: target) }
                }
            }
            .onChange(of: transcript) {
                generation &+= 1
                placedLeadingRow = leadingRow
                following = true
                position.scrollTo(edge: .bottom)
            }
    }

    // Geometry callbacks run mid-layout, so scrolling waits for the next turn and is dropped if the transcript changed meanwhile.
    private func settle(_ move: @escaping (inout ScrollPosition) -> Void) {
        let scheduled = generation
        Task { @MainActor in
            guard scheduled == generation else { return }
            move(&position)
        }
    }
}

extension View {
    func transcriptFollow<ID: Equatable>(_ transcript: ID, leadingRow: String?, position: Binding<ScrollPosition>, following: Binding<Bool>) -> some View {
        modifier(TranscriptFollow(transcript: transcript, leadingRow: leadingRow, position: position, following: following))
    }
}

@MainActor
func scrollToLatest(_ position: Binding<ScrollPosition>, following: Binding<Bool>) {
    following.wrappedValue = true
    withAnimation(.easeOut(duration: 0.25)) { position.wrappedValue.scrollTo(edge: .bottom) }
}
