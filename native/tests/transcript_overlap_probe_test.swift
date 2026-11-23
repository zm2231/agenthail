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
        let order = Dictionary(uniqueKeysWithValues: ids.enumerated().map { ($1, $0) })
        let _ = tailRevision
        let split = feed.variant == 2 ? ids.count : layout.tail.split(ids, in: feed.transcript)
        let _ = feed.split = split
        ScrollView {
            Group {
                if feed.variant == 1 {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(feed.rows.prefix(split)) { row in
                        RowView(row: row, index: order[row.id] ?? 0).padding(.top, 14).reportsTop(row.id, to: tops)
                    }
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(feed.rows.dropFirst(split)) { row in
                        RowView(row: row, index: order[row.id] ?? 0).padding(.top, 14)
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
                            RowView(row: row, index: order[row.id] ?? 0).padding(.top, 14).reportsTop(row.id, to: tops)
                        }
                    }
                } else {
                    VStack(alignment: .leading, spacing: 0) {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(feed.rows.prefix(split)) { row in
                        RowView(row: row, index: order[row.id] ?? 0).padding(.top, 14).reportsTop(row.id, to: tops)
                    }
                        }
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(feed.rows.dropFirst(split)) { row in
                        RowView(row: row, index: order[row.id] ?? 0).padding(.top, 14)
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

// Each row paints its place in the transcript into its background, so the colors down the view are the order rows were drawn in.
struct RowView: View {
    let row: Row
    let index: Int

    var body: some View {
        Text(String(repeating: "transcript line\n", count: row.lines) + row.id)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(.sRGB, red: Double(index % 64 * 4) / 255, green: Double(index / 64 % 64 * 4) / 255, blue: 200 / 255))
    }
}

// An agent's live turn: tool calls that show a line each until the third folds the run to one summary line, command output,
// messages from other agents, a steered message landing before the reply, the run moving to just before the turn's latest reply
// as Focus places it, a streaming reply, and the queue tray changing the bottom inset.
@MainActor
struct LiveTurn {
    var generator: SplitMix
    var next = 0
    var run: (id: String, calls: Int)?

    let prefix: String

    init(seed: UInt64) {
        generator = SplitMix(seed: seed)
        prefix = "t\(seed)-"
    }

    mutating func step(_ feed: Feed) {
        func add(_ kind: String, _ lines: Int) -> String {
            next += 1
            let id = "\(prefix)\(kind)\(next)"
            feed.rows.append(Row(id: id, lines: lines))
            return id
        }
        switch generator.next() % 12 {
        case 0..<3:
            if let current = run, let index = feed.rows.firstIndex(where: { $0.id == current.id }) {
                let calls = current.calls + 1
                run = (current.id, calls)
                feed.rows[index].lines = calls <= 2 ? calls : 1
            } else {
                run = (add("run", 1), 1)
            }
        case 3:
            _ = add("out", 3 + Int(generator.next() % 22))
        case 4:
            _ = add("peer", 1)
        case 5:
            _ = add("reply", 2 + Int(generator.next() % 7))
            run = nil
        case 6, 7:
            feed.rows[feed.rows.count - 1].lines += 1 + Int(generator.next() % 3)
        case 8:
            if let current = run, let index = feed.rows.firstIndex(where: { $0.id == current.id }), index < feed.rows.count - 2 {
                let moved = feed.rows.remove(at: index)
                feed.rows.insert(moved, at: feed.rows.count - 1)
            }
        case 9:
            next += 1
            feed.rows.insert(Row(id: "\(prefix)steer\(next)", lines: 1), at: feed.rows.count - 1)
        default:
            feed.inset = feed.inset == 100 ? 170 : 100
        }
    }
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

@main
struct TranscriptLayoutProbe {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        var report: [String] = []
        for variant in 0..<3 {
            for seed in [589, 1, 2] {
                let feed = Feed()
                feed.variant = variant
                feed.transcript = "V\(variant)-\(seed)"
                let hosting = NSHostingView(rootView: Transcript(feed: feed))
                let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 800, height: 900), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
                window.alphaValue = 0
                window.ignoresMouseEvents = true
                window.contentView = hosting
                spin(0.2)
                let scroll = scrollView(in: hosting)!
                feed.rows = rows("S\(seed)-", 400)
                spin(0.8)
                var turn = LiveTurn(seed: UInt64(seed))
                var misdrawnSteps = 0
                var unreachable = 0
                var first = ""
                for _ in 0..<300 {
                    turn.step(feed)
                    spin(0.02 + Double(turn.generator.next() % 200) / 1000)
                    if misdrawn(hosting, feed) != nil {
                        spin(0.4)
                        if let still = misdrawn(hosting, feed) {
                            misdrawnSteps += 1
                            if first.isEmpty { first = still }
                        }
                    }
                    let clip = scroll.contentView
                    let end = (scroll.documentView?.frame.height ?? 0) + clip.contentInsets.bottom - clip.bounds.maxY
                    if let last = feed.rows.last, let top = tops.top(last.id), top > (scroll.documentView?.frame.height ?? 0) - 10, end < 1 { unreachable += 1 }
                }
                report.append("variant \(variant) seed \(seed): misdrawn \(misdrawnSteps) unreachable \(unreachable) \(first.prefix(300))")
                window.contentView = nil
                spin(0.2)
            }
        }
        print(report.joined(separator: "\n"))
        exit(1)
    }

    @MainActor
    static func drawn(_ view: NSView, top: CGFloat, bottom: CGFloat) -> [Int] {
        guard let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds),
              let space = CGColorSpace(name: CGColorSpace.sRGB) else { return [] }
        view.cacheDisplay(in: view.bounds, to: rep)
        guard let image = rep.cgImage,
              let context = CGContext(data: nil, width: image.width, height: image.height, bitsPerComponent: 8, bytesPerRow: image.width * 4,
                                      space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue),
              let data = context.data else { return [] }
        context.draw(image, in: CGRect(x: 0, y: 0, width: image.width, height: image.height))
        let scale = CGFloat(image.height) / view.bounds.height
        let x = Int((view.bounds.width - 32) * scale)
        var runs: [Int] = []
        for line in Int(top * scale)..<(image.height - Int(bottom * scale)) {
            let at = (line * image.width + x) * 4
            let r = Int(data.load(fromByteOffset: at, as: UInt8.self)), g = Int(data.load(fromByteOffset: at + 1, as: UInt8.self)), b = Int(data.load(fromByteOffset: at + 2, as: UInt8.self))
            guard abs(b - 200) <= 3 else { continue }
            let index = (r + 2) / 4 + 64 * ((g + 2) / 4)
            if runs.last != index { runs.append(index) }
        }
        let falls = zip(runs, runs.dropFirst()).filter { $0 > $1 }.count
        return falls * 2 > runs.count ? runs.reversed() : runs
    }

    @MainActor
    static func misdrawn(_ view: NSView, _ feed: Feed) -> String? {
        let runs = drawn(view, top: 50, bottom: feed.inset + 6)
        guard !runs.isEmpty else { return "nothing drawn" }
        for (a, b) in zip(runs, runs.dropFirst()) where b != a + 1 {
            let name = { (i: Int) in i < feed.rows.count ? feed.rows[i].id : "?\(i)" }
            let side = { (i: Int) in i < feed.split ? "lazy" : "eager" }
            return "\(name(a)) (\(side(a))) then \(name(b)) (\(side(b))) down the view, drawn \(runs.map(name)), split \(feed.split)/\(feed.rows.count)"
        }
        return nil
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
        spin(0.4)
    }

    @MainActor
    static func offset(_ scroll: NSScrollView) -> CGFloat {
        scroll.contentView.bounds.origin.y
    }
}
