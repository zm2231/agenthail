import SwiftUI

struct TranscriptFollow<ID: Equatable>: ViewModifier {
    let transcript: ID
    let holdBottom: Bool
    @Binding var position: ScrollPosition
    @Binding var following: Bool
    @State private var phase: ScrollPhase = .idle

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
                if old.contentSize == new.contentSize && old.containerSize == new.containerSize {
                    // A jump's animation passes through offsets short of the bottom; they are not the reader leaving it.
                    if phase != .animating { following = Self.atBottom(new) }
                } else if following, phase == .idle || phase == .animating, !Self.atBottom(new) {
                    Task { @MainActor in position.scrollTo(edge: .bottom) }
                } else if !following, holdBottom, phase == .idle {
                    let target = max(new.contentSize.height - (old.contentSize.height - old.contentOffset.y), 0)
                    Task { @MainActor in position.scrollTo(y: target) }
                }
            }
            .onChange(of: transcript) {
                following = true
                position.scrollTo(edge: .bottom)
            }
    }
}

extension View {
    func transcriptFollow<ID: Equatable>(_ transcript: ID, holdBottom: Bool = false, position: Binding<ScrollPosition>, following: Binding<Bool>) -> some View {
        modifier(TranscriptFollow(transcript: transcript, holdBottom: holdBottom, position: position, following: following))
    }
}

@MainActor
func scrollToLatest(_ position: Binding<ScrollPosition>, following: Binding<Bool>) {
    following.wrappedValue = true
    withAnimation(.easeOut(duration: 0.25)) { position.wrappedValue.scrollTo(edge: .bottom) }
}
