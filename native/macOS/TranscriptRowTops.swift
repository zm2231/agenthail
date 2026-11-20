import SwiftUI

// A reference, so a row reporting its place does not redraw the transcript.
@MainActor
final class TranscriptRowTops {
    private var tops: [String: CGFloat] = [:]
    private var shown: [String: Int] = [:]
    var moved: ((String) -> Void)?
    var holdingOlder: (() -> Void)?

    func set(_ id: String, top: CGFloat) {
        tops[id] = top
        moved?(id)
    }

    // A lazy stack can build a row's replacement before the old one disappears, so a row is laid out while any copy of it is.
    func appeared(_ id: String) {
        shown[id, default: 0] += 1
        moved?(id)
    }

    func disappeared(_ id: String) {
        shown[id] = max((shown[id] ?? 0) - 1, 0)
        moved?(id)
    }

    func top(_ id: String) -> CGFloat? { (shown[id] ?? 0) > 0 ? tops[id] : nil }

    var leading: (id: String, top: CGFloat)? {
        tops.filter { (shown[$0.key] ?? 0) > 0 }.min { $0.value < $1.value }.map { ($0.key, $0.value) }
    }

    func holdOlderPlace() { holdingOlder?() }
}

extension View {
    func reportsTop(_ id: String, to tops: TranscriptRowTops?) -> some View {
        onGeometryChange(for: CGFloat.self) { $0.frame(in: .named(TranscriptRows.contentSpace)).minY } action: { tops?.set(id, top: $0) }
            .onAppear { tops?.appeared(id) }
            .onDisappear { tops?.disappeared(id) }
    }
}
