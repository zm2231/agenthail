import SwiftUI

/// Stops following the live edge and holds the current offset, so a row a person expands or collapses stays under the pointer.
/// Equal for the life of a transcript, so rows that read it are not re-rendered on every scroll update.
struct HoldTranscriptPlace: Equatable {
    fileprivate var hold: (@MainActor () -> Void)?

    @MainActor func callAsFunction() { hold?() }

    static func == (lhs: Self, rhs: Self) -> Bool { (lhs.hold == nil) == (rhs.hold == nil) }
}

extension EnvironmentValues {
    @Entry var holdTranscriptPlace = HoldTranscriptPlace()
}

struct TranscriptFollow<ID: Equatable>: ViewModifier {
    let transcript: ID
    let leadingRow: String?
    let hasRow: (String) -> Bool
    @Binding var position: ScrollPosition
    @Binding var following: Bool
    @State private var phase: ScrollPhase = .idle
    @State private var generation = 0
    @State private var placedLeadingRow: String?
    @State private var layout = Layout()

    static var bottomSlack: CGFloat { 70 }
    /// Longer than a jump's animation. A scroll moved mid-animation can leave the phase animating for good.
    static var animationLimit: Duration { .milliseconds(500) }

    /// `scrollTo(y:)` measures from below the top inset, such as the conversation header; content offsets include it.
    static func scrollY(_ offset: CGFloat, in geometry: ScrollGeometry) -> CGFloat {
        offset + geometry.contentInsets.top
    }

    static func atBottom(_ geometry: ScrollGeometry) -> Bool {
        geometry.contentOffset.y + geometry.containerSize.height >= geometry.contentSize.height - bottomSlack
    }

    func body(content: Content) -> some View {
        content
            .scrollPosition($position)
            .defaultScrollAnchor(.bottom, for: .initialOffset)
            .defaultScrollAnchor(following ? .bottom : nil, for: .sizeChanges)
            .onScrollPhaseChange { _, new in
                phase = new
                layout.animationStart = new == .animating ? .now : nil
                if new == .animating {
                    let start = layout.animationStart
                    Task { @MainActor in
                        try? await Task.sleep(for: Self.animationLimit)
                        if layout.animationStart == start { returnToBottom() }
                    }
                } else if new == .idle {
                    returnToBottom()
                }
            }
            .onScrollGeometryChange(for: ScrollGeometry.self) { geometry in
                // The action is not called for every change, so the latest geometry is kept as it is read.
                layout.geometry = geometry
                return geometry
            } action: { old, new in
                let inserted = leadingRow != placedLeadingRow && placedLeadingRow.map(hasRow) == true
                placedLeadingRow = leadingRow
                let resized = old.contentSize != new.contentSize || old.containerSize != new.containerSize
                if resized { layout.markResized() }
                if !resized {
                    if following, resting, layout.resized, !Self.atBottom(new) {
                        // Layout that moves the offset in the same pass as a resize, as a lazy stack re-measuring rows does, is not the reader leaving the bottom.
                        settle { $0.scrollTo(edge: .bottom) }
                    } else if !animating {
                        // A jump's animation passes through offsets short of the bottom; they are not the reader leaving it.
                        following = Self.atBottom(new)
                    }
                } else if following, phase == .idle || phase == .animating, !Self.atBottom(new) {
                    settle { $0.scrollTo(edge: .bottom) }
                } else if !following, inserted, resting {
                    let target = max(new.contentSize.height - (old.contentSize.height - old.contentOffset.y), 0)
                    settle { $0.scrollTo(y: Self.scrollY(target, in: new)) }
                } else if !following, resting, Self.atBottom(new) {
                    // Content that shrank to the reader's position, or now fits, leaves nothing to jump to.
                    following = true
                }
            }
            .environment(\.holdTranscriptPlace, HoldTranscriptPlace(hold: holdPlace))
            .onChange(of: transcript) {
                generation &+= 1
                placedLeadingRow = leadingRow
                following = true
                position.scrollTo(edge: .bottom)
            }
    }

    private var animating: Bool {
        guard phase == .animating, let start = layout.animationStart else { return false }
        return start.duration(to: .now) < Self.animationLimit
    }

    private var resting: Bool { phase == .idle || (phase == .animating && !animating) }

    // A scroll position still at the bottom edge keeps the bottom in place as content grows, even when not following.
    private func holdPlace() {
        following = false
        if let geometry = layout.geometry { position.scrollTo(y: Self.scrollY(geometry.contentOffset.y, in: geometry)) }
    }

    // Updates during an animation are ignored, so one that comes to rest short of the bottom is checked once it has.
    private func returnToBottom() {
        guard following, let geometry = layout.geometry, !Self.atBottom(geometry) else { return }
        settle { $0.scrollTo(edge: .bottom) }
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

/// The latest scroll geometry, when an animated scroll began, and whether the layout pass now running resized the transcript.
/// A reference, so updating it does not redraw the transcript.
@MainActor
private final class Layout {
    var geometry: ScrollGeometry?
    var animationStart: ContinuousClock.Instant?
    private(set) var resized = false

    func markResized() {
        guard !resized else { return }
        resized = true
        DispatchQueue.main.async { self.resized = false }
    }
}

extension View {
    func transcriptFollow<ID: Equatable>(_ transcript: ID, leadingRow: String?, hasRow: @escaping (String) -> Bool, position: Binding<ScrollPosition>, following: Binding<Bool>) -> some View {
        modifier(TranscriptFollow(transcript: transcript, leadingRow: leadingRow, hasRow: hasRow, position: position, following: following))
    }
}

@MainActor
func scrollToLatest(_ position: Binding<ScrollPosition>, following: Binding<Bool>) {
    following.wrappedValue = true
    withAnimation(.easeOut(duration: 0.25)) { position.wrappedValue.scrollTo(edge: .bottom) }
}
