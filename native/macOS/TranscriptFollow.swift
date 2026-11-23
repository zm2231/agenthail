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
    let held: () -> Bool
    var rows: TranscriptRowTops?
    @Binding var position: ScrollPosition
    @Binding var following: Bool
    var rescaling = false
    @State private var phase: ScrollPhase = .idle
    @State private var generation = 0
    @State private var placedLeadingRow: String?
    @State private var layout = Layout()

    static var bottomSlack: CGFloat { TranscriptFollowing.bottomSlack }
    static var readerReach: CGFloat { TranscriptFollowing.readerReach }
    /// Longer than a jump's animation. A scroll moved mid-animation can leave the phase animating for good.
    static var animationLimit: Duration { .milliseconds(500) }
    static var inputLimit: Duration { .milliseconds(400) }
    static var inputCap: Duration { .seconds(10) }

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
            .onScrollPhaseChange { old, new in
                phase = new
                // Keyboard paging and scrollbar clicks animate; a jump to the end does too, but moves toward it.
                if old == .idle, new != .idle { layout.gestureStart = layout.geometry?.contentOffset.y }
                if new == .idle, old != .idle { layout.input = nil }
                if new == .idle, old != .idle, let geometry = layout.geometry {
                    let moved = layout.gestureStart.map { geometry.contentOffset.y - $0 } ?? 0
                    let wasFollowing = following
                    following = TranscriptFollowing.next(following, .init(distance: TranscriptEnd(geometry).distance, scrollEnded: true, offsetMoved: moved))
                    if wasFollowing, !following { position.scrollTo(y: Self.scrollY(geometry.contentOffset.y, in: geometry)) }
                    layout.gestureStart = nil
                }
                if new == .interacting || new == .decelerating { layout.releaseHold() }
                // A reader who scrolls while an older page loads is held where they come to rest.
                if new == .idle, old == .interacting || old == .decelerating, layout.olderPending { holdOlderPlace(fresh: false) }
                if new == .idle, let geometry = layout.geometry { settleAnchor(anchorStep(geometry, inserted: false), in: geometry) }
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
                if let previous = layout.geometry, previous.contentSize.height != geometry.contentSize.height {
                    layout.lastGrowth = geometry.contentSize.height - previous.contentSize.height
                }
                layout.geometry = geometry
                if following { layout.scheduleCheck(checkEnd) }
                return geometry
            } action: { old, new in
                layout.seen = new
                let inserted = leadingRow != placedLeadingRow && placedLeadingRow.map(hasRow) == true
                placedLeadingRow = leadingRow
                let resized = old.contentSize != new.contentSize || old.containerSize != new.containerSize
                if resized { layout.markResized(from: old.contentOffset.y) }
                if let rows, let id = layout.row.pass(TranscriptPass(leadingChanged: inserted, resting: resting, following: following, content: new.contentSize.height)) {
                    restoreHeld(id, in: rows)
                    return
                }
                if layout.revealed?.expired == true { layout.revealed = nil }
                if !following, resting, let target = layout.revealed?.target(in: new, resized: resized) {
                    settle { $0.scrollTo(y: target) }
                    return
                }
                if !following, !inserted, resting, let target = layout.heldTarget(new, rows: rows) {
                    settleHold { $0.scrollTo(y: Self.scrollY(target, in: new)) }
                    return
                }
                if settleAnchor(anchorStep(new, inserted: inserted), in: new) { return }
                let pass = TranscriptFollowing.Pass(distance: TranscriptEnd(new).distance, resized: resized, resizedThisTurn: layout.resized, resting: resting,
                                                    animating: animating, inserted: inserted, offsetMoved: new.contentOffset.y - old.contentOffset.y,
                                                    reset: new.contentOffset.y <= -new.contentInsets.top + 0.5, overshot: TranscriptEnd(old).distance < -0.5)
                let wasFollowing = following
                // Jump to latest during an input wins over it.
                if following { layout.input = nil }
                // A reader's input holds following off until it ends, however near the end its first steps leave them.
                following = layout.input == nil && TranscriptFollowing.next(following, pass)
                // A pin left the position at the bottom edge; once the reader leaves, it holds where they are so it is not applied again.
                if !following, wasFollowing || layout.leftByInput {
                    layout.leftByInput = false
                    position.scrollTo(y: Self.scrollY(new.contentOffset.y, in: new))
                }
                if !resized {
                    if following, resting, layout.resized, !Self.atBottom(new) {
                        // Layout that moves the offset in the same pass as a resize, as a lazy stack re-measuring rows does, is not the reader leaving the bottom.
                        pinToEnd()
                    }
                } else if following, !rescaling, phase == .idle || phase == .animating, TranscriptEnd(new).distance > 0.5 {
                    // With no size-change anchor, a following reader is kept at the end here, in the pass that grew the content.
                    position.scrollTo(edge: .bottom)
                } else if !following, inserted, resting, layout.holdingRow {
                    // Rows below the reader that were never laid out are re-estimated as a page arrives, so the end moves; the held row does not.
                } else if !following, inserted, resting {
                    let distance = layout.heldFromEnd ?? (old.contentSize.height - old.contentOffset.y)
                    layout.holdFromEnd(distance, content: new.contentSize.height)
                    let target = max(new.contentSize.height - distance, 0)
                    settleHold { $0.scrollTo(y: Self.scrollY(target, in: new)) }
                }
            }
            .environment(\.holdTranscriptPlace, HoldTranscriptPlace(hold: holdPlace))
            .environment(\.transcriptClip, layout.clip)
            .environment(\.revealRow, RowAction(owner: String(describing: transcript)) { top in
                following = false
                layout.input = nil
                layout.releaseHold()
                layout.revealed = RevealedRow(top: top)
                position.scrollTo(y: top.wrappedValue)
            })
            .onAppear {
                rows?.moved = { rowMoved($0) }
                layout.clip.readerLeaves = { readerLeaves() }
                rows?.holdingOlder = { holdOlderPlace(fresh: true) }
            }
            .onDisappear { layout.cancelCheck() }
            // A page can land without changing the scroll geometry, so the geometry action may not see it.
            .onChange(of: leadingRow) { old, new in
                guard old.map(hasRow) == true, let rows,
                      let id = layout.row.pass(TranscriptPass(leadingChanged: true, resting: resting, following: following, content: layout.geometry?.contentSize.height ?? 0)) else { return }
                placedLeadingRow = new
                restoreHeld(id, in: rows)
            }
            .onChange(of: transcript) {
                generation &+= 1
                layout.input = nil
                layout.forgetOlder()
                layout.cancelCheck()
                layout.lastGrowth = nil
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

    // Armed when the reader asks for an older page, from the row frames already reported, so nothing is inferred while the page lands.
    private func holdOlderPlace(fresh: Bool) {
        guard !following else { return }
        guard let geometry = layout.geometry, let leading = rows?.leading(above: geometry.contentOffset.y + geometry.containerSize.height) else { return layout.requestOlder(fresh: fresh) }
        layout.armRow(leading.id, above: leading.top - geometry.contentOffset.y, before: leading.top, fresh: fresh)
    }

    private func rowMoved(_ id: String) {
        if id == layout.anchor.id, let geometry = layout.geometry, settleAnchor(anchorStep(geometry, inserted: false), in: geometry) { return }
        guard let rows, !following, layout.holdingRow, id == layout.heldRow else { return }
        restoreHeld(id, in: rows)
    }

    // Only a reader at rest with no other hold in force; anything else lets the next resting pass pick the row afresh.
    private func anchorStep(_ geometry: ScrollGeometry, inserted: Bool) -> TranscriptRowAnchor.Step {
        guard let rows, !following, !inserted, resting, !layout.holding, layout.revealed == nil, !layout.olderPending else {
            layout.anchor.release()
            return .none
        }
        let offset = geometry.contentOffset.y
        let visible = geometry.containerSize.height + geometry.contentInsets.top + geometry.contentInsets.bottom
        let range = -geometry.contentInsets.top...max(geometry.contentSize.height + geometry.contentInsets.bottom - geometry.containerSize.height - geometry.contentInsets.top, -geometry.contentInsets.top)
        let seen = layout.resized ? layout.turnOffset ?? offset : offset
        return layout.anchor.pass(offset: offset, range: range, top: rows.top) {
            rows.inView(from: seen, to: seen + visible, beforeTurn: layout.resized).map { ($0.id, $0.top, $0.top - seen) }
        }
    }

    // A correction whose move was undone, whose row was replaced, or whose reader has since jumped to the end, is dropped before it runs.
    @discardableResult
    private func settleAnchor(_ step: TranscriptRowAnchor.Step, in geometry: ScrollGeometry) -> Bool {
        switch step {
        case .none: return false
        case .cancel: layout.dropRestore(); return false
        case .scroll(let target):
            let id = layout.anchor.id
            settleHold { position in
                guard !following, layout.anchor.id == id else { return }
                position.scrollTo(y: Self.scrollY(target, in: geometry))
            }
            return true
        }
    }

    // A held row that is not laid out, or still has its pre-insert top, is built by scrolling to it; it reports again once laid out.
    private func restoreHeld(_ id: String, in rows: TranscriptRowTops) {
        settleHold { position in
            guard layout.heldRow == id else { return }
            if rows.top(id).map({ !layout.row.measured && $0 == layout.heldBefore }) ?? true {
                position.scrollTo(id: id, anchor: .top)
            } else {
                correct(in: rows)
            }
        }
    }

    private func correct(in rows: TranscriptRowTops) {
        guard let geometry = layout.geometry, let target = layout.heldTarget(geometry, rows: rows) else { return }
        settleHold { $0.scrollTo(y: Self.scrollY(target, in: geometry)) }
    }

    // A scroll position still at the bottom edge keeps the bottom in place as content grows, even when not following.
    private func holdPlace() {
        following = false
        layout.input = nil
        layout.releaseHold()
        if let geometry = layout.geometry { position.scrollTo(y: Self.scrollY(geometry.contentOffset.y, in: geometry)) }
    }

    private func readerLeaves() {
        let now = ContinuousClock.now
        if layout.input != nil {
            layout.input?.last = now
            return
        }
        guard following, let start = layout.geometry?.contentOffset.y else { return }
        following = false
        layout.leftByInput = true
        let input = ReaderScroll(start: start, last: now)
        layout.input = input
        Task { @MainActor in await settleInput(input.id) }
    }

    // Each input renews the hold; it ends with its scroll phase, or once input stops outside a gesture, and never outlives the cap.
    private func settleInput(_ id: UUID) async {
        while let input = layout.input, input.id == id {
            try? await Task.sleep(for: Self.inputLimit)
            guard let input = layout.input, input.id == id, !following, let geometry = layout.geometry else { return }
            let quiet = input.last.duration(to: .now) >= Self.inputLimit
            let gesture = phase == .interacting || phase == .decelerating
            guard quiet && !gesture || input.started.duration(to: .now) >= Self.inputCap else { continue }
            layout.input = nil
            following = abs(geometry.contentOffset.y - input.start) < 0.5 || Self.atEnd(geometry)
            if following { pinToEnd() }
        }
    }

    // A reader who scrolled away after a pin was queued keeps their place.
    private func pinToEnd() {
        settle { position in
            if following { position.scrollTo(edge: .bottom) }
        }
    }

    // Updates during an animation are ignored, so one that comes to rest short of the bottom is checked once it has.
    private func returnToBottom() {
        guard following, let geometry = layout.geometry, !Self.atBottom(geometry) else { return }
        pinToEnd()
    }

    // The geometry action can skip a stream's last change; false asks again once the scroll rests.
    private func checkEnd() -> Bool {
        guard let geometry = layout.geometry else { return true }
        switch TranscriptEndCheck.decide(following: following, resting: resting, geometry: geometry, seen: layout.seen, deferred: layout.checkDeferred,
                                         lastGrowth: layout.lastGrowth, clipDistance: layout.clip.distanceToEnd, held: held()) {
        case .retry:
            if resting { layout.checkDeferred = true }
            return false
        case .scroll:
            pinToEnd()
            return true
        case .clip:
            layout.clip.scrollToEnd()
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

    // Dropped when a newer restore was queued or the hold was released or replaced; a hold that only ran out of time while it waited still gets it.
    private func settleHold(_ move: @escaping (inout ScrollPosition) -> Void) {
        let queued = layout.queueRestore()
        settle { position in
            guard layout.runRestore(queued) else { return }
            move(&position)
        }
    }
}

private struct ReaderScroll {
    let id = UUID()
    let start: CGFloat
    let started = ContinuousClock.now
    var last: ContinuousClock.Instant
}

/// The latest scroll geometry, when an animated scroll began, and whether the layout pass now running resized the transcript.
/// A reference, so updating it does not redraw the transcript.
@MainActor
private final class Layout {
    var geometry: ScrollGeometry?
    var seen: ScrollGeometry?
    var checkDeferred = false
    let clip = TranscriptClip()
    var lastGrowth: CGFloat?
    var animationStart: ContinuousClock.Instant?
    var gestureStart: CGFloat?
    var leftByInput = false
    var input: ReaderScroll?
    private var resizedTurn: Int?
    private var fromEnd: (distance: CGFloat, until: ContinuousClock.Instant, content: CGFloat, landing: Bool)?
    let row = TranscriptRowHold()
    let anchor = TranscriptRowAnchor()
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
        row.release()
        renew()
        fromEnd = (distance, .now + .seconds(2), content, true)
    }

    private var sequence = 0

    private func renew() {
        sequence &+= 1
        anchor.release()
    }

    // Only the newest queued restore runs, and a busy main thread can run it after the settle window, so the window starts once it has.
    func queueRestore() -> Int {
        sequence &+= 1
        row.settle(until: nil)
        fromEnd?.until = .now + .seconds(86_400)
        return sequence
    }

    func dropRestore() { sequence &+= 1 }

    func runRestore(_ queued: Int) -> Bool {
        guard queued == sequence else { return false }
        row.settle(until: .now + .seconds(2))
        fromEnd?.until = .now + .seconds(2)
        return true
    }

    var holding: Bool { holdingRow || heldFromEnd != nil }

    var holdingRow: Bool { row.holding }
    var heldRow: String? { row.id }
    var heldBefore: CGFloat? { row.before }
    var olderPending: Bool { row.pending }

    func requestOlder(fresh: Bool) {
        renew()
        fromEnd = nil
        row.request(fresh: fresh)
    }

    func armRow(_ id: String, above: CGFloat, before: CGFloat, fresh: Bool) {
        renew()
        fromEnd = nil
        row.arm(id, above: above, before: before, fresh: fresh)
    }

    func forgetOlder() {
        row.forget()
        releaseHold()
    }

    func releaseHold() {
        renew()
        fromEnd = nil
        row.release()
        revealed = nil
    }

    /// Earlier rows keep re-measuring after they arrive, moving the rows below them without scrolling, and the geometry action
    /// can miss such a resize, so the height is compared with the last one handled. Any other move while settled is the reader's.
    func heldTarget(_ geometry: ScrollGeometry, rows: TranscriptRowTops?) -> CGFloat? {
        if row.landed {
            let target = row.target(top: row.id.flatMap { rows?.top($0) }, offset: geometry.contentOffset.y, content: geometry.contentSize.height)
            if row.id == nil { releaseHold() }
            return target
        }
        guard let distance = heldFromEnd else { return nil }
        let content = geometry.contentSize.height
        if content != fromEnd?.content {
            fromEnd?.content = content
            fromEnd?.landing = true
            return max(content - distance, 0)
        }
        if abs(content - geometry.contentOffset.y - distance) <= 1 {
            fromEnd?.landing = false
        } else if fromEnd?.landing == true {
            return max(content - distance, 0)
        } else {
            releaseHold()
        }
        return nil
    }

    var resized: Bool { resizedTurn == RunLoopTurn.current }
    private(set) var turnOffset: CGFloat?

    func markResized(from offset: CGFloat) {
        if !resized { turnOffset = offset }
        resizedTurn = RunLoopTurn.current
    }
}

/// Counts main run loop passes; unlike a main-queue hop, it moves on even while the main queue is too busy to run a block.
@MainActor
enum RunLoopTurn {
    private static var turn = 0
    private static let observer: CFRunLoopObserver? = {
        let observer = CFRunLoopObserverCreateWithHandler(nil, CFRunLoopActivity.beforeTimers.rawValue, true, 0) { _, _ in
            MainActor.assumeIsolated { turn &+= 1 }
        }
        CFRunLoopAddObserver(CFRunLoopGetMain(), observer, .commonModes)
        return observer
    }()

    static var current: Int {
        _ = observer
        return turn
    }
}

extension View {
    func transcriptFollow<ID: Equatable>(_ transcript: ID, leadingRow: String?, hasRow: @escaping (String) -> Bool, held: @escaping () -> Bool = { false }, rows: TranscriptRowTops? = nil, position: Binding<ScrollPosition>, following: Binding<Bool>, rescaling: Bool = false) -> some View {
        modifier(TranscriptFollow(transcript: transcript, leadingRow: leadingRow, hasRow: hasRow, held: held, rows: rows, position: position, following: following, rescaling: rescaling))
    }
}

@MainActor
func scrollToLatest(_ position: Binding<ScrollPosition>, following: Binding<Bool>, latest: String?) {
    following.wrappedValue = true
    guard let latest else {
        withAnimation(.easeOut(duration: 0.25)) { position.wrappedValue.scrollTo(edge: .bottom) }
        return
    }
    // A lazy stack can leave rows it never laid out unbuilt after an edge scroll; scrolling to the row first builds them.
    position.wrappedValue.scrollTo(id: latest, anchor: .bottom)
    DispatchQueue.main.async {
        guard position.wrappedValue.viewID(type: String.self) == latest else { return }
        position.wrappedValue.scrollTo(edge: .bottom)
    }
}
