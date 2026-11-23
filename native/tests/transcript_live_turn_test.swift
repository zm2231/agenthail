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
        let split = layout.tail.split(ids, in: feed.transcript)
        let _ = feed.split = split
        ScrollView {
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

    @discardableResult
    mutating func add(_ kind: String, _ lines: Int, to feed: Feed) -> String {
        next += 1
        let id = "\(prefix)\(kind)\(next)"
        feed.rows.append(Row(id: id, lines: lines))
        return id
    }

    mutating func call(_ feed: Feed) {
        if let current = run, let index = feed.rows.firstIndex(where: { $0.id == current.id }) {
            let calls = current.calls + 1
            run = (current.id, calls)
            feed.rows[index].lines = calls <= 2 ? calls : 1
        } else {
            run = (add("run", 1, to: feed), 1)
        }
    }

    func relocate(_ feed: Feed) {
        guard let current = run, let index = feed.rows.firstIndex(where: { $0.id == current.id }), index < feed.rows.count - 2 else { return }
        let moved = feed.rows.remove(at: index)
        feed.rows.insert(moved, at: feed.rows.count - 1)
    }

    mutating func step(_ feed: Feed) {
        switch generator.next() % 12 {
        case 0..<3:
            call(feed)
        case 3:
            add("out", 3 + Int(generator.next() % 22), to: feed)
        case 4:
            add("peer", 1, to: feed)
        case 5:
            add("reply", 2 + Int(generator.next() % 7), to: feed)
            run = nil
        case 6, 7:
            feed.rows[feed.rows.count - 1].lines += 1 + Int(generator.next() % 3)
        case 8:
            relocate(feed)
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
struct TranscriptLiveTurnTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let feed = Feed()
        let hosting = NSHostingView(rootView: Transcript(feed: feed))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 800, height: 900), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = hosting
        spin(0.2)
        let scroll = scrollView(in: hosting)!

        feed.transcript = "F"
        feed.rows = rows("F", 400)
        spin(0.8)
        following(hosting, feed)

        feed.transcript = "R"
        feed.rows = rows("R", 400)
        spin(0.8)
        reading(scroll, hosting, feed)

        print(failures == 0 ? "ok" : "\(failures) failures")
        exit(failures == 0 ? 0 : 1)
    }

    // A reader following the end of a long transcript while the live turn runs.
    @MainActor
    static func following(_ hosting: NSView, _ feed: Feed) {
        var turn = LiveTurn(seed: 589)
        var found: [String] = []
        for _ in 0..<300 where found.count < 3 {
            turn.step(feed)
            spin(0.02 + Double(turn.generator.next() % 200) / 1000)
            if misdrawn(hosting, feed) != nil {
                spin(0.4)
                if let still = misdrawn(hosting, feed) { found.append(still) }
            }
        }
        expect(found.isEmpty, "a following reader sees the rows in view drawn once each and in order while a live turn runs: \(found.joined(separator: "; "))")
    }

    // A reader scrolled back to a turn's tool run while the turn goes on: its calls fold, it moves to before the latest reply, and rows keep arriving below.
    @MainActor
    static func reading(_ scroll: NSScrollView, _ hosting: NSView, _ feed: Feed) {
        var turn = LiveTurn(seed: 1259)
        var worst: (row: String, moved: CGFloat) = ("", 0)
        var found: [String] = []
        for round in 0..<6 where found.count < 3 {
            turn.add("reply", 3, to: feed)
            turn.run = nil
            turn.call(feed)
            guard let run = turn.run?.id else { return expect(false, "round \(round): the turn has a tool run") }
            for _ in 0..<6 { turn.add("out", 20, to: feed) }
            turn.add("reply", 4, to: feed)
            spin(0.6)
            for _ in 0..<10 where tops.top(run) == nil { move(scroll, to: max(offset(scroll) - 600, 0)) }
            guard let runTop = tops.top(run) else { expect(false, "round \(round): the tool run is laid out once the reader scrolls back to it"); continue }
            move(scroll, to: runTop - 120)
            expect(!feed.following, "round \(round): a reader scrolled back to the tool run is not following")
            guard let index = feed.rows.firstIndex(where: { $0.id == run }),
                  let reader = feed.rows[(index + 1)...].first(where: { tops.top($0.id).map { $0 > offset(scroll) + 150 } == true })?.id,
                  let start = tops.top(reader) else { expect(false, "round \(round): a row below the tool run is in view"); continue }
            let placed = start - offset(scroll)
            NotificationCenter.default.post(name: NSScrollView.willStartLiveScrollNotification, object: scroll)
            spin(0.1)
            expect(feed.phase == .interacting, "round \(round): a live scroll puts the transcript in its interacting phase, got \(feed.phase)")
            func check() {
                let moved = abs((tops.top(reader) ?? .infinity) - offset(scroll) - placed)
                if moved > worst.moved { worst = (reader, moved) }
                if let wrong = misdrawn(hosting, feed) { found.append(wrong) }
            }
            for _ in 0..<2 {
                turn.call(feed)
                spin(0.15)
                check()
            }
            turn.relocate(feed)
            spin(0.15)
            check()
            for _ in 0..<8 {
                turn.step(feed)
                spin(0.02 + Double(turn.generator.next() % 150) / 1000)
                check()
            }
            NotificationCenter.default.post(name: NSScrollView.didEndLiveScrollNotification, object: scroll)
            spin(0.4)
            move(scroll, to: scroll.documentView?.frame.height ?? 0)
        }
        expect(worst.moved < 2, "a reader's row holds its place on screen while they scroll and the tool run above it folds and moves, \(worst.row) moved \(worst.moved)")
        expect(found.isEmpty, "a scrolling reader sees the rows in view drawn once each and in order while a live turn runs: \(found.joined(separator: "; "))")
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
