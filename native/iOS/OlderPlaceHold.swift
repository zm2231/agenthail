import SwiftUI

// A reference, so tracking geometry does not redraw the transcript.
@MainActor
final class OlderPlaceHold {
    static let quiet: Duration = .milliseconds(500)
    static let longest: Duration = .seconds(3)

    var scroll: (ScrollPosition) -> Void = { _ in }
    private var leading: (id: String, frame: CGRect)?
    private var geometry: ScrollGeometry?
    private var held: (id: String, top: CGFloat)?
    private var heldFrame: CGRect?
    private var generation = 0
    private var lastChange = ContinuousClock.now
    private var settling: Task<Void, Never>?

    private var shown: Set<String> = []

    func isHeld(_ id: String) -> Bool { held?.id == id }

    func shows(_ id: String, _ shown: Bool) {
        if shown { self.shown.insert(id) } else { self.shown.remove(id) }
        if held?.id == id { correct() }
    }

    func track(_ id: String, frame: CGRect) {
        if held?.id == id {
            heldFrame = frame
            lastChange = .now
            correct()
        } else {
            leading = (id, frame)
        }
    }

    func hold(_ id: String?) -> Int {
        release()
        if let id, let leading, leading.id == id {
            held = (id, leading.frame.minY)
            heldFrame = leading.frame
        }
        return generation
    }

    func pageApplied(_ hold: Int) {
        guard hold == generation, held != nil else { return }
        lastChange = .now
        let giveUp = ContinuousClock.now + Self.longest
        settling = Task { [weak self] in
            while let self, self.held != nil {
                let quietUntil = self.lastChange + Self.quiet
                if ContinuousClock.now >= quietUntil || ContinuousClock.now >= giveUp { self.release(); return }
                try? await Task.sleep(until: min(quietUntil, giveUp))
                if Task.isCancelled { return }
            }
        }
    }

    func release() {
        generation &+= 1
        settling?.cancel()
        settling = nil
        held = nil
        heldFrame = nil
    }

    func read(_ geometry: ScrollGeometry) { self.geometry = geometry }

    func contentChanged() {
        guard held != nil else { return }
        lastChange = .now
        correct()
    }

    private func correct() {
        guard held != nil else { return }
        let scheduled = generation
        Task { @MainActor in
            guard scheduled == self.generation, let held = self.held, let geometry = self.geometry else { return }
            var position = ScrollPosition()
            if self.shown.contains(held.id), let frame = self.heldFrame {
                let shift = frame.minY - held.top
                guard abs(shift) > 0.5 else { return }
                position.scrollTo(y: geometry.contentOffset.y + geometry.contentInsets.top + shift)
            } else {
                position.scrollTo(id: held.id, anchor: .top)
            }
            self.scroll(position)
        }
    }
}

extension View {
    func tracksOlderPlace(_ id: String, leading: Bool, in hold: OlderPlaceHold) -> some View {
        background {
            if leading || hold.isHeld(id) {
                Color.clear
                    .onGeometryChange(for: CGRect.self) { $0.frame(in: .scrollView) } action: { hold.track(id, frame: $0) }
                    .onAppear { hold.shows(id, true) }
                    .onDisappear { hold.shows(id, false) }
            }
        }
    }
}
