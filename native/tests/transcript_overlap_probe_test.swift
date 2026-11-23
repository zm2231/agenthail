// comment-checker-disable-file
@testable import AgenthailHelpers
import AppKit
import SwiftUI

struct Row: Identifiable, Equatable {
    let id: String
    var lines: Int
}

@MainActor
final class Feed: ObservableObject {
    @Published var transcript = "A"
    @Published var rows: [Row] = []
    @Published var inset: CGFloat = 100
    var following = true
    var split = 0
    var phase: ScrollPhase = .idle
    var variant = 0
}

@MainActor var frames: [String: CGRect] = [:]
@MainActor var live: Set<String> = []
@MainActor var visible: CGRect = .zero
@MainActor let tops = TranscriptRowTops()
@MainActor let memory = TranscriptRowMemory()

func rows(_ prefix: String, _ count: Int) -> [Row] {
    (0..<count).map { Row(id: "\(prefix)\($0)", lines: 1 + ($0 * 7) % 9) }
}

// The desktop transcript's layout: settled rows in a lazy stack, the newest in an eager stack below it.
struct Transcript: View {
    @ObservedObject var feed: Feed
    @State private var position = ScrollPosition(edge: .bottom)
    @State private var following = true
    @State private var layout = TranscriptTailLayout()
    @State private var tailRevision = 0

    var body: some View {
        let ids = feed.rows.map(\.id)
        let _ = tailRevision
        let split = feed.variant == 2 ? ids.count : layout.tail.split(ids, in: feed.transcript)
        let _ = feed.split = split
        ScrollView {
            Group {
                if feed.variant == 1 {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(feed.rows.prefix(split)) { row in
                        RowView(row: row).padding(.top, 14).reportsTop(row.id, to: tops)
                    }
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(feed.rows.dropFirst(split)) { row in
                        RowView(row: row).padding(.top, 14)
                            .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(TranscriptRows.contentSpace)) } action: {
                                layout.rows[row.id] = $0
                                cover(ids)
                            }
                            .onDisappear { layout.rows[row.id] = nil }
                            .reportsTop(row.id, to: tops)
                    }
                        }
                    }
                } else if feed.variant == 2 {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(feed.rows) { row in
                            RowView(row: row).padding(.top, 14).reportsTop(row.id, to: tops)
                        }
                    }
                } else {
                    VStack(alignment: .leading, spacing: 0) {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(feed.rows.prefix(split)) { row in
                        RowView(row: row).padding(.top, 14).reportsTop(row.id, to: tops)
                    }
                        }
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(feed.rows.dropFirst(split)) { row in
                        RowView(row: row).padding(.top, 14)
                            .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(TranscriptRows.contentSpace)) } action: {
                                layout.rows[row.id] = $0
                                cover(ids)
                            }
                            .onDisappear { layout.rows[row.id] = nil }
                            .reportsTop(row.id, to: tops)
                    }
                        }
                    }
                }
            }
            .padding(24)
            .coordinateSpace(.named(TranscriptRows.contentSpace))
            .background { TranscriptClipAnchor().frame(width: 0, height: 0) }
            .environment(\.transcriptRows, TranscriptRows(memory: memory, session: feed.transcript))
        }
        .onScrollGeometryChange(for: CGRect.self) { $0.visibleRect } action: { _, rect in
            layout.visible = rect
            visible = rect
            cover(ids)
        }
        .onScrollPhaseChange { _, new in
            feed.phase = new
            layout.scrolling = new == .interacting || new == .decelerating
            if !layout.scrolling { cover(ids) }
        }
        .onChange(of: "\(feed.transcript)|\(ids.count)|\(ids.last ?? "")", initial: true) {
            layout.tail.update(ids, in: feed.transcript, rows: layout.rows, visible: layout.visible, following: following)
        }
        .onChange(of: feed.rows.first?.id, initial: true) { tops.first = feed.rows.first?.id }
        .transcriptFollow(feed.transcript, leadingRow: feed.rows.first?.id, hasRow: { id in feed.rows.contains { $0.id == id } }, rows: tops, position: $position, following: $following)
        .safeAreaInset(edge: .top, spacing: 0) { Color.gray.frame(height: 44) }
        .safeAreaInset(edge: .bottom, spacing: 0) { Color.gray.frame(height: feed.inset) }
        .onChange(of: following) { feed.following = following }
    }

    private func cover(_ ids: [String]) {
        guard following, layout.tail.cover(ids, in: feed.transcript, rows: layout.rows, visible: layout.visible, resting: !layout.scrolling) else { return }
        tailRevision &+= 1
    }
}

struct RowView: View {
    let row: Row

    var body: some View {
        Text(String(repeating: "transcript line\n", count: row.lines) + row.id)
            .frame(maxWidth: .infinity, alignment: .leading)
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(TranscriptRows.contentSpace)) } action: { frames[row.id] = $0 }
            .onAppear { live.insert(row.id) }
            .onDisappear { live.remove(row.id) }
    }
}

@main
struct TranscriptOverlapProbe {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        var report: [String] = []
        for variant in 0..<3 {
            frames = [:]
            live = []
            let feed = Feed()
            feed.variant = variant
            feed.transcript = "V\(variant)"
            let hosting = NSHostingView(rootView: Transcript(feed: feed))
            let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 800, height: 900), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
            window.alphaValue = 0
            window.ignoresMouseEvents = true
            window.contentView = hosting
            spin(0.2)
            let scroll = scrollView(in: hosting)!
            for seed in [589, 1, 2] {
                feed.transcript = "V\(variant)-\(seed)"
                feed.rows = rows("S\(seed)-", 400)
                spin(0.8)
                let (overlaps, unreachable, first) = stream(scroll, feed, seed: UInt64(seed))
                report.append("variant \(variant) seed \(seed): overlaps \(overlaps) unreachable \(unreachable) \(first)")
            }
            window.contentView = nil
            spin(0.2)
        }
        print(report.joined(separator: "\n"))
        exit(1)
    }

    @MainActor
    static func stream(_ scroll: NSScrollView, _ feed: Feed, seed: UInt64) -> (Int, Int, String) {
        var generator = SplitMix(seed: seed)
        var next = 0
        var overlaps = 0
        var unreachable = 0
        var first = ""
        for _ in 0..<300 {
            switch generator.next() % 10 {
            case 0..<5:
                next += 1
                feed.rows.append(Row(id: "Sn\(seed)-\(next)", lines: 1 + Int(generator.next() % 14)))
            case 5..<8:
                feed.rows[feed.rows.count - 1].lines += 1 + Int(generator.next() % 3)
            case 8:
                feed.inset = feed.inset == 100 ? 170 : 100
            default:
                for _ in 0..<(2 + Int(generator.next() % 5)) {
                    next += 1
                    feed.rows.append(Row(id: "Sn\(seed)-\(next)", lines: 1 + Int(generator.next() % 14)))
                }
            }
            spin(0.02 + Double(generator.next() % 200) / 1000)
            if overlap(feed) != nil {
                spin(0.4)
                if let still = overlap(feed) {
                    overlaps += 1
                    if first.isEmpty { first = still }
                }
            }
            spin(0.05)
            let document = scroll.documentView?.frame.height ?? 0
            if let newest = feed.rows.last.flatMap({ frames[$0.id] }), live.contains(feed.rows.last!.id), newest.maxY > document + 1 {
                spin(0.4)
                if let again = frames[feed.rows.last!.id], again.maxY > (scroll.documentView?.frame.height ?? 0) + 1 { unreachable += 1 }
            }
        }
        return (overlaps, unreachable, first)
    }

    @MainActor
    static func overlap(_ feed: Feed) -> String? {
        let shown = feed.rows.enumerated().filter { live.contains($0.element.id) && frames[$0.element.id] != nil }
        for (a, b) in zip(shown, shown.dropFirst()) {
            let fa = frames[a.element.id]!, fb = frames[b.element.id]!
            guard fa.maxY > visible.minY, fb.minY < visible.maxY, fb.minY < fa.maxY - 1 else { continue }
            let side = { (index: Int) in index < feed.split ? "lazy" : "eager" }
            return "\(a.element.id) (\(side(a.offset)) \(Int(fa.minY))-\(Int(fa.maxY))) under \(b.element.id) (\(side(b.offset)) \(Int(fb.minY))-\(Int(fb.maxY))), split \(feed.split)/\(feed.rows.count), view \(Int(visible.minY))-\(Int(visible.maxY))"
        }
        return nil
    }

    struct SplitMix {
        var seed: UInt64
        mutating func next() -> UInt64 {
            seed &+= 0x9E37_79B9_7F4A_7C15
            var z = seed
            z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
            z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
            return z ^ (z >> 31)
        }
    }

    @MainActor static var failures = 0

    @MainActor
    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            failures += 1
            print("FAIL: \(message)")
        }
    }

    static func scrollView(in view: NSView) -> NSScrollView? {
        if let scroll = view as? NSScrollView { return scroll }
        return view.subviews.lazy.compactMap { scrollView(in: $0) }.first
    }

    static func spin(_ seconds: Double) {
        RunLoop.main.run(until: Date().addingTimeInterval(seconds))
    }

    @MainActor
    static func move(_ scroll: NSScrollView, to y: CGFloat) {
        scroll.contentView.scroll(to: NSPoint(x: 0, y: y))
        scroll.reflectScrolledClipView(scroll.contentView)
        spin(0.3)
    }

    @MainActor
    static func offset(_ scroll: NSScrollView) -> CGFloat {
        scroll.contentView.bounds.origin.y
    }
}
