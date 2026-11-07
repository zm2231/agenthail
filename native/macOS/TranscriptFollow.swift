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
    @Entry var transcriptOverhang: CGFloat = 0
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
    static var readerReach: CGFloat { 4 }
    /// Longer than a jump's animation. A scroll moved mid-animation can leave the phase animating for good.
    static var animationLimit: Duration { .milliseconds(500) }

    /// `scrollTo(y:)` measures from below the top inset, such as the conversation header; content offsets include it.
    static func scrollY(_ offset: CGFloat, in geometry: ScrollGeometry) -> CGFloat {
        offset + geometry.contentInsets.top
    }

    static func atBottom(_ geometry: ScrollGeometry) -> Bool {
        TranscriptEnd(geometry).distance < bottomSlack
    }

    // The slack absorbs layout settling; following is regained, or kept after a reader's scroll, only at the end itself.
    static func atEnd(_ geometry: ScrollGeometry) -> Bool {
        TranscriptEnd(geometry).distance < readerReach
    }

    func body(content: Content) -> some View {
        content
            .scrollPosition($position)
            .defaultScrollAnchor(.bottom, for: .initialOffset)
            .defaultScrollAnchor(following ? .bottom : nil, for: .sizeChanges)
            .onScrollPhaseChange { old, new in
                phase = new
                if new == .idle, old == .interacting || old == .decelerating, let geometry = layout.geometry {
                    following = Self.atEnd(geometry)
                }
                if new == .interacting || new == .decelerating { layout.releaseHold() }
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
                if following { layout.scheduleCheck(checkEnd) }
                return geometry
            } action: { old, new in
                layout.seen = new
                let inserted = leadingRow != placedLeadingRow && placedLeadingRow.map(hasRow) == true
                placedLeadingRow = leadingRow
                let resized = old.contentSize != new.contentSize || old.containerSize != new.containerSize
                if resized { layout.markResized() }
                if layout.revealed?.expired == true { layout.revealed = nil }
                if !following, resting, let target = layout.revealed?.target(in: new, resized: resized) {
                    settle { $0.scrollTo(y: target) }
                    return
                }
                if !following, !inserted, resting, let target = layout.heldTarget(new) {
                    settle { $0.scrollTo(y: Self.scrollY(target, in: new)) }
                    return
                }
                if !resized {
                    if following, resting, layout.resized, !Self.atBottom(new) {
                        // Layout that moves the offset in the same pass as a resize, as a lazy stack re-measuring rows does, is not the reader leaving the bottom.
                        settle { $0.scrollTo(edge: .bottom) }
                    } else if !animating {
                        // A jump's animation passes through offsets short of the bottom; they are not the reader leaving it.
                        following = following ? Self.atBottom(new) : Self.atEnd(new)
                    }
                } else if following, phase == .idle || phase == .animating, !Self.atBottom(new) {
                    settle { $0.scrollTo(edge: .bottom) }
                } else if !following, inserted, resting {
                    let distance = layout.heldFromEnd ?? (old.contentSize.height - old.contentOffset.y)
                    layout.holdFromEnd(distance, content: new.contentSize.height)
                    let target = max(new.contentSize.height - distance, 0)
                    settle { $0.scrollTo(y: Self.scrollY(target, in: new)) }
                } else if !following, resting, Self.atEnd(new) {
                    // Content that shrank to the reader's position, or now fits, leaves nothing to jump to.
                    following = true
                }
            }
            .environment(\.holdTranscriptPlace, HoldTranscriptPlace(hold: holdPlace))
            .environment(\.revealRow) { top in
                following = false
                layout.releaseHold()
                layout.revealed = RevealedRow(top: top)
                position.scrollTo(y: top.wrappedValue)
            }
            .onDisappear { layout.cancelCheck() }
            .onChange(of: transcript) {
                generation &+= 1
                layout.releaseHold()
                layout.cancelCheck()
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
        layout.releaseHold()
        if let geometry = layout.geometry { position.scrollTo(y: Self.scrollY(geometry.contentOffset.y, in: geometry)) }
    }

    // Updates during an animation are ignored, so one that comes to rest short of the bottom is checked once it has.
    private func returnToBottom() {
        guard following, let geometry = layout.geometry, !Self.atBottom(geometry) else { return }
        settle { $0.scrollTo(edge: .bottom) }
    }

    // The geometry action can skip a stream's last change; false asks again once the scroll rests.
    private func checkEnd() -> Bool {
        guard let geometry = layout.geometry else { return true }
        switch TranscriptEndCheck.decide(following: following, resting: resting, geometry: geometry, seen: layout.seen, deferred: layout.checkDeferred) {
        case .retry:
            if resting { layout.checkDeferred = true }
            return false
        case .scroll:
            settle { $0.scrollTo(edge: .bottom) }
            return true
        case .done:
            return true
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

/// The latest scroll geometry, when an animated scroll began, and whether the layout pass now running resized the transcript.
/// A reference, so updating it does not redraw the transcript.
@MainActor
private final class Layout {
    var geometry: ScrollGeometry?
    var seen: ScrollGeometry?
    var checkDeferred = false
    var animationStart: ContinuousClock.Instant?
    private(set) var resized = false
    private var fromEnd: (distance: CGFloat, until: ContinuousClock.Instant, content: CGFloat, landing: Bool)?
    var revealed: RevealedRow?
    private var check: Task<Void, Never>?

    // Not pushed back by later changes, so a steady stream is still checked.
    func scheduleCheck(_ run: @escaping @MainActor () -> Bool) {
        guard check == nil else { return }
        checkDeferred = false
        check = Task { @MainActor in
            repeat {
                do { try await Task.sleep(for: .milliseconds(150)) } catch { return }
            } while !run()
            check = nil
        }
    }

    func cancelCheck() {
        check?.cancel()
        check = nil
        checkDeferred = false
    }

    var heldFromEnd: CGFloat? {
        guard let fromEnd, ContinuousClock.now < fromEnd.until else { return nil }
        return fromEnd.distance
    }

    func holdFromEnd(_ distance: CGFloat, content: CGFloat) {
        fromEnd = (distance, .now + .seconds(2), content, true)
    }

    func releaseHold() {
        fromEnd = nil
        revealed = nil
    }

    /// Earlier rows keep re-measuring after they arrive, moving the rows below them without scrolling, and the geometry action
    /// can miss such a resize, so the height is compared with the last one handled. Any other move while settled is the reader's.
    func heldTarget(_ geometry: ScrollGeometry) -> CGFloat? {
        guard let distance = heldFromEnd else { return nil }
        let content = geometry.contentSize.height
        if content != fromEnd?.content {
            fromEnd?.content = content
            fromEnd?.landing = true
            return max(content - distance, 0)
        }
        if abs(content - geometry.contentOffset.y - distance) <= 1 {
            fromEnd?.landing = false
        } else if fromEnd?.landing == false {
            releaseHold()
        }
        return nil
    }

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
