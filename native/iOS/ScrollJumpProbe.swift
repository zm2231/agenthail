import SwiftUI
#if DEBUG
import QuartzCore

@MainActor
final class ScrollJumpProbe {
    static let enabled = ProcessInfo.processInfo.arguments.contains("--scroll-probe")
    static let shared = ScrollJumpProbe()
    static var file: URL { URL(fileURLWithPath: NSTemporaryDirectory()).appendingPathComponent("scroll-probe.tsv") }

    private var offset: CGFloat = 0
    private var height: CGFloat = 0
    private var visible: CGFloat = 0
    private var phase = "idle"
    private var rows: [String: CGFloat] = [:]
    private var last: (offset: CGFloat, height: CGFloat, rows: [String: CGFloat])?
    private var touched = false
    private var fingerSteps: [CGFloat] = []
    private var lines = ["t\tphase\toffset\theight\tshift\tstep\tmoved"]
    private var ticker: Ticker?
    private let started = CACurrentMediaTime()
    private(set) var jumps = 0
    private(set) var worst: CGFloat = 0

    func geometry(_ geometry: ScrollGeometry) {
        offset = geometry.contentOffset.y
        height = geometry.contentSize.height
        visible = geometry.visibleRect.height
        if ticker == nil { ticker = Ticker { [weak self] in self?.tick() } }
    }

    func stop() {
        flush()
        ticker?.invalidate()
        ticker = nil
        last = nil
    }

    func phase(_ phase: ScrollPhase) {
        self.phase = "\(phase)"
        if phase == .interacting { touched = true }
        if phase == .idle { flush() }
    }

    private var heights: [String: CGFloat] = [:]
    private var lastHeights: [String: CGFloat] = [:]

    func row(_ id: String, frame: CGRect) {
        rows[id] = frame.minY
        heights[id] = frame.height
    }

    func gone(_ id: String) {
        rows[id] = nil
        heights[id] = nil
    }

    private func layout(_ last: [String: CGFloat]) -> String {
        rows.sorted { $0.value < $1.value }.map { id, minY in
            let mark = last[id] == nil ? "+" : (lastHeights[id].map { abs($0 - heights[id]!) > 1 } ?? false ? "~" : "")
            return "\(mark)\(id)@\(Int(minY))h\(Int(heights[id] ?? 0))"
        }.joined(separator: " ")
    }

    private func tick() {
        defer {
            last = (offset, height, rows)
            lastHeights = heights
        }
        guard let last else { return }
        let shown = rows.filter { $0.value - offset > -20 && $0.value - offset < visible }
        let shifts = shown.keys.compactMap { id in last.rows[id].map { rows[id]! - $0 } }.sorted()
        let shift = shifts.isEmpty ? 0 : shifts[shifts.count / 2]
        let moved = rows.filter { id, minY in last.rows[id].map { abs($0 - minY) > 1 } ?? false }.min { $0.value < $1.value }
        let step = offset - last.offset
        if abs(step) > 1000 {
            let changed = heights.compactMap { id, now in lastHeights[id].map { now - $0 } }.filter { abs($0) > 1 }
            let added = rows.keys.filter { last.rows[$0] == nil }.count
            let gone = last.rows.keys.filter { rows[$0] == nil }.count
            log.append(String(format: "t=%.2f BIG step=%.0f h=%.0f->%.0f rowsDelta=%.0f changed=%d new=%d gone=%d phase=%@ notes[%@]",
                              CACurrentMediaTime() - started, step, last.height, height, changed.reduce(0, +), changed.count, added, gone, phase, recentNotes()))
        }
        guard abs(shift) > 0.1 || abs(step) > 0.1 || moved != nil || abs(height - last.height) > 0.1 else { return }
        let recent = fingerSteps.suffix(3).sorted()
        let finger = recent.isEmpty ? 0 : recent[recent.count / 2]
        let seen = abs(shift) > 2 ? shift - (step - finger) : 0
        if abs(shift) <= 2 {
            fingerSteps.append(step)
            if fingerSteps.count > 6 { fingerSteps.removeFirst() }
        }
        if touched, abs(seen) > 2 {
            jumps += 1
            worst = max(worst, abs(seen))
            log.append(summary(seen: seen, step: step, last: last.rows))
            lines.append("#before \(last.rows.sorted { $0.value < $1.value }.map { "\($0.key)@\(Int($0.value))h\(Int(lastHeights[$0.key] ?? 0))" }.joined(separator: " "))")
            lines.append("#after \(layout(last.rows))")
        }
        lines.append(String(format: "%.3f\t%@\t%.1f\t%.1f\t%.1f\t%.1f\t%@", CACurrentMediaTime() - started, phase, offset, height, shift, step, moved?.key ?? "-"))
    }

    private(set) var log: [String] = []
    private var notes: [(at: CFTimeInterval, what: String)] = []

    func note(_ what: String) {
        notes.append((CACurrentMediaTime(), what))
        if notes.count > 12 { notes.removeFirst() }
    }

    private func recentNotes() -> String {
        let now = CACurrentMediaTime()
        return notes.filter { now - $0.at < 0.5 }.map { String(format: "%@@%.0fms", $0.what, (now - $0.at) * 1000) }.joined(separator: ",")
    }

    private func summary(seen: CGFloat, step: CGFloat, last: [String: CGFloat]) -> String {
        let above = rows.filter { $0.value < offset }
        let grown = above.compactMap { id, _ -> String? in
            guard last[id] != nil, let now = heights[id], let before = lastHeights[id], abs(now - before) > 1 else { return nil }
            return "\(id)\(now > before ? "+" : "")\(Int(now - before))"
        }
        let added = above.keys.filter { last[$0] == nil }
        return String(format: "t=%.2f seen=%.0f step=%.0f h=%.0f", CACurrentMediaTime() - started, seen, step, height)
            + " grew[\(grown.joined(separator: ","))] new[\(added.count)] notes[\(recentNotes())]"
    }

    private func flush() {
        try? lines.joined(separator: "\n").write(to: Self.file, atomically: true, encoding: .utf8)
    }
}

@MainActor
private final class Ticker: NSObject {
    private var link: CADisplayLink?
    private let action: @MainActor () -> Void

    init(_ action: @escaping @MainActor () -> Void) {
        self.action = action
        super.init()
        link = CADisplayLink(target: self, selector: #selector(fire))
        link?.add(to: .main, forMode: .common)
    }

    func invalidate() {
        link?.invalidate()
        link = nil
    }

    @objc private func fire() { action() }
}

struct ScrollJumpLabel: View {
    var body: some View {
        TimelineView(.periodic(from: .now, by: 0.25)) { _ in
            Text("jumps=\(ScrollJumpProbe.shared.jumps) worst=\(Int(ScrollJumpProbe.shared.worst))")
                .font(.system(size: 1)).opacity(0.01)
                .frame(width: 1, height: 1).clipped()
                .accessibilityIdentifier("scroll-jumps")
            Text(ScrollJumpProbe.shared.log.suffix(10).joined(separator: " ; "))
                .font(.system(size: 1)).opacity(0.01)
                .frame(width: 1, height: 1).clipped()
                .accessibilityIdentifier("scroll-jump-log")
        }
    }
}
#endif

@MainActor func probeNote(_ what: @autoclosure () -> String) {
#if DEBUG
    if ScrollJumpProbe.enabled { ScrollJumpProbe.shared.note(what()) }
#endif
}

extension View {
    @ViewBuilder func probesScrollJump(_ id: String) -> some View {
#if DEBUG
        if ScrollJumpProbe.enabled {
            background {
                Color.clear
                    .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(TranscriptRows.contentSpace)) } action: { ScrollJumpProbe.shared.row(id, frame: $0) }
                    .onDisappear { ScrollJumpProbe.shared.gone(id) }
            }
        } else { self }
#else
        self
#endif
    }

    @ViewBuilder func scrollJumpProbe() -> some View {
#if DEBUG
        if ScrollJumpProbe.enabled {
            onScrollGeometryChange(for: ScrollGeometry.self) { $0 } action: { _, geometry in ScrollJumpProbe.shared.geometry(geometry) }
                .onScrollPhaseChange { _, phase in ScrollJumpProbe.shared.phase(phase) }
                .onDisappear { ScrollJumpProbe.shared.stop() }
        } else { self }
#else
        self
#endif
    }
}
