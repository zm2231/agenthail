@testable import AgenthailHelpers
import AppKit
import SwiftUI

final class SelectedText: NSView {
    @objc var hasSelectedText = true
}

struct Row: Identifiable {
    let id: String
    let lines: Int
}

@MainActor
final class Feed: ObservableObject {
    @Published var transcript = "A"
    @Published var rows: [Row] = []
    @Published var inset: CGFloat = 100
    @Published var header: CGFloat = 44
    @Published var usesTail = false
    var following = true
    let hold = TranscriptSelectionHold()
    var eager = 0
    var crossed = 0
}

@MainActor var renders: [String: Int] = [:]
@MainActor var appears: [String: Int] = [:]
@MainActor var shownOpen: [String: Bool] = [:]
@MainActor var rowTops: [String: CGFloat] = [:]
@MainActor var seenGeometry: [String] = []
@MainActor var live: Set<String> = []
@MainActor let rowMemory = TranscriptRowMemory()
@MainActor weak var watched: Feed?

func rows(_ prefix: String, _ count: Int) -> [Row] {
    (0..<count).map { Row(id: "\(prefix)\($0)", lines: 1 + ($0 * 7) % 9) }
}

struct Transcript: View {
    @ObservedObject var feed: Feed
    @State private var position = ScrollPosition(edge: .bottom)
    @State private var following = true
    @State private var layout = TranscriptTailLayout()

    var body: some View {
        let ids = feed.rows.map(\.id)
        let split = feed.usesTail ? layout.tail.split(ids, in: feed.transcript) : ids.count
        let _ = feed.eager = ids.count - split
        let _ = feed.crossed = max(feed.crossed, layout.tail.crossed)
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(feed.rows.prefix(split)) { row in
                        RowView(row: row)
                            .padding(.top, 20)
                    }
                }
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(feed.rows.dropFirst(split)) { row in
                        RowView(row: row)
                            .padding(.top, 20)
                            .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(TranscriptRows.contentSpace)) } action: { layout.rows[row.id] = $0 }
                            .onDisappear { layout.rows[row.id] = nil }
                    }
                }
            }
            .padding(24)
            .coordinateSpace(.named(TranscriptRows.contentSpace))
            .background { TranscriptSelectionAnchor(hold: feed.hold) }
            .background { TranscriptClipAnchor().frame(width: 0, height: 0) }
            .environment(\.transcriptRows, TranscriptRows(memory: rowMemory, session: feed.transcript))
        }
        .onScrollGeometryChange(for: Bool.self) { geometry in
            layout.visible = geometry.visibleRect
            seenGeometry.append(String(format: "%.3f offset %.0f content %.0f visible maxY %.0f insets %.0f/%.0f end %.0f", Date().timeIntervalSince1970.truncatingRemainder(dividingBy: 100), geometry.contentOffset.y, geometry.contentSize.height, geometry.visibleRect.maxY, geometry.contentInsets.top, geometry.contentInsets.bottom, geometry.contentSize.height - (geometry.visibleRect.maxY - geometry.contentInsets.bottom)))
            if seenGeometry.count > 12 { seenGeometry.removeFirst() }
            return true
        } action: { _, _ in }
        .onChange(of: "\(feed.transcript)|\(ids.count)|\(ids.last ?? "")", initial: true) {
            guard feed.usesTail else { return }
            layout.tail.update(ids, in: feed.transcript, rows: layout.rows, visible: layout.visible, following: following) { feed.hold.held(over: $0, buttons: 0) }
        }
        .transcriptFollow(feed.transcript, leadingRow: feed.rows.first?.id, hasRow: { id in feed.rows.contains { $0.id == id } }, position: $position, following: $following)
        .safeAreaInset(edge: .top, spacing: 0) { Color.gray.frame(height: feed.header) }
        .safeAreaInset(edge: .bottom, spacing: 0) { Color.gray.frame(height: feed.inset) }
        .onChange(of: following) {
            feed.following = following
            seenGeometry.append(String(format: "%.3f following %@", Date().timeIntervalSince1970.truncatingRemainder(dividingBy: 100), following ? "on" : "off"))
            if seenGeometry.count > 12 { seenGeometry.removeFirst() }
        }
        .onReceive(NotificationCenter.default.publisher(for: .jumpToLatest)) { _ in
            scrollToLatest($position, following: $following, latest: feed.rows.last?.id)
        }
    }
}

struct RowView: View {
    let row: Row
    @State private var opened: RowChoice<Bool>?
    @Environment(\.transcriptRows) private var memory
    @Environment(\.holdTranscriptPlace) private var holdPlace

    var body: some View {
        let _ = renders[row.id, default: 0] += 1
        let expanded = memory.current(opened) ?? memory.isOpen(row.id) ?? false
        let _ = shownOpen[row.id] = expanded
        Text(String(repeating: "transcript line\n", count: row.lines + (expanded ? 30 : 0)) + row.id)
            .frame(maxWidth: .infinity, alignment: .leading)
            .onGeometryChange(for: CGFloat.self) { $0.frame(in: .named(TranscriptRows.contentSpace)).minY } action: { rowTops[row.id] = $0 }
            .onAppear {
                appears[row.id, default: 0] += 1
                live.insert(row.id)
            }
            .onDisappear { live.remove(row.id) }
            .onReceive(NotificationCenter.default.publisher(for: .toggleRow)) { note in
                guard note.object as? String == row.id else { return }
                holdPlace()
                opened = memory.choice(!expanded)
                memory.set(row.id, open: !expanded)
            }
            .onReceive(NotificationCenter.default.publisher(for: .collapseRow)) { note in
                guard note.object as? String == row.id else { return }
                opened = memory.choice(false)
                memory.set(row.id, open: false)
            }
            .background {
                if row.id == "Zc98" { RevealOnCollapse(id: row.id) }
            }
    }
}

struct RevealOnCollapse: View {
    let id: String
    @Environment(\.revealRow) private var revealRow

    var body: some View {
        Color.clear.onReceive(NotificationCenter.default.publisher(for: .collapseRow)) { note in
            guard note.object as? String == id else { return }
            revealRow(Binding(get: { rowTops[id] ?? 0 }, set: { _ in }))
        }
    }
}

extension Notification.Name {
    static let jumpToLatest = Notification.Name("jumpToLatest")
    static let toggleRow = Notification.Name("toggleRow")
    static let collapseRow = Notification.Name("collapseRow")
}

@main
struct TranscriptFollowTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let feed = Feed()
        watched = feed
        let hosting = NSHostingView(rootView: Transcript(feed: feed))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 800, height: 600), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.alphaValue = 0
        window.ignoresMouseEvents = true
        window.contentView = hosting
        spin(0.2)
        let scroll = scrollView(in: hosting)!

        feed.rows = rows("A", 60)
        spin(0.5)
        expectBottom(scroll, "a transcript whose first page loads after it opens shows the live edge")

        feed.transcript = "B"
        feed.rows = []
        spin(0.1)
        feed.rows = rows("B", 60)
        spin(0.5)
        expectBottom(scroll, "switching to a transcript that loads after the switch shows the live edge")

        feed.transcript = "C"
        feed.rows = rows("C", 40)
        spin(0.1)
        feed.rows = rows("C", 60)
        spin(0.5)
        expectBottom(scroll, "switching to a cached transcript that then refreshes shows the live edge")

        move(scroll, to: 200)
        feed.rows += rows("Cn", 3)
        spin(0.5)
        expect(offset(scroll) == 200 && !feed.following, "a reader who scrolled up stays put when new rows arrive, got offset \(offset(scroll))")

        move(scroll, to: 400)
        let fromBottom = gap(scroll)
        feed.rows = rows("Co", 10) + feed.rows
        expectEventually(abs(gap(scroll) - fromBottom) < 2, "loading earlier rows keeps the reader's place, distance from bottom \(fromBottom) became \(gap(scroll))")
        spin(0.6)
        expect(abs(gap(scroll) - fromBottom) < 2, "the reader's place holds after earlier rows settle, distance from bottom \(fromBottom) became \(gap(scroll))")

        move(scroll, to: 500)
        feed.rows = rows("Cr", 70)
        expectEventually(offset(scroll) == 500 && !feed.following, "a page that replaces the rows leaves an up-scrolled reader where they were, got offset \(offset(scroll))")
        spin(0.6)
        expect(offset(scroll) == 500 && !feed.following, "an up-scrolled reader stays put after a replaced page settles, got offset \(offset(scroll))")

        move(scroll, to: 300)
        feed.rows = rows("Cq", 10) + feed.rows
        feed.transcript = "C2"
        feed.rows = rows("C2", 50)
        spin(0.8)
        expectBottom(scroll, "switching while earlier rows were being placed in the previous transcript shows the new live edge")

        NotificationCenter.default.post(name: .jumpToLatest, object: nil)
        spin(0.8)
        expectBottom(scroll, "jump to latest reaches the bottom")

        feed.rows += rows("Cm", 3)
        spin(0.5)
        expectBottom(scroll, "a following reader sees new rows")

        feed.inset = 260
        spin(0.4)
        expectBottom(scroll, "a following reader stays at the bottom when the composer grows")
        feed.inset = 100
        spin(0.4)

        move(scroll, to: 300)
        feed.transcript = "D"
        feed.rows = []
        spin(0.1)
        feed.rows = rows("D", 80)
        spin(0.6)
        expectBottom(scroll, "switching after scrolling up in another transcript shows the live edge")

        feed.transcript = "E"
        feed.rows = rows("E", 600)
        spin(0.6)
        expectBottom(scroll, "a long transcript opens at the live edge")
        move(scroll, to: 0)
        NotificationCenter.default.post(name: .jumpToLatest, object: nil)
        spin(1.0)
        expectBottom(scroll, "jump to latest from the top of a long lazy transcript reaches the bottom")

        move(scroll, to: 0)
        NotificationCenter.default.post(name: .jumpToLatest, object: nil)
        spin(0.08)
        feed.rows += rows("Ej", 2)
        spin(0.08)
        feed.rows += rows("Ek", 2)
        spin(1.0)
        expectBottom(scroll, "jump to latest reaches the bottom when rows arrive during the jump")

        feed.transcript = "G"
        feed.rows = []
        spin(0.1)
        feed.rows = rows("G", 4)
        spin(0.3)
        for page in 0..<4 {
            feed.rows = rows("Gp\(page)-", 20) + feed.rows
            spin(0.35)
        }
        expectBottom(scroll, "a short transcript that fills the viewport with earlier pages stays at the live edge")

        for index in 0..<5 {
            feed.rows += rows("Es\(index)-", 1)
            spin(0.15)
        }
        spin(0.4)
        expectBottom(scroll, "a following reader sees streamed rows")

        feed.transcript = "H"
        feed.rows = rows("H", 40)
        spin(0.6)
        expectBottom(scroll, "a fresh transcript shows the live edge")
        let pinned = offset(scroll)
        NotificationCenter.default.post(name: .toggleRow, object: "H38")
        spin(0.6)
        expect(abs(offset(scroll) - pinned) < 2 && !feed.following, "expanding a row while following keeps it where it was clicked, offset \(pinned) became \(offset(scroll))")
        NotificationCenter.default.post(name: .toggleRow, object: "H38")
        spin(0.6)
        expect(abs(offset(scroll) - pinned) < 2, "collapsing it again keeps it where it was clicked, offset \(pinned) became \(offset(scroll))")

        feed.transcript = "K"
        feed.rows = rows("K", 3)
        spin(0.4)
        NotificationCenter.default.post(name: .toggleRow, object: "K1")
        spin(0.4)
        move(scroll, to: 0)
        expect(!feed.following, "a reader scrolled to the top of an expanded row is not following")
        NotificationCenter.default.post(name: .toggleRow, object: "K1")
        spin(0.6)
        expect(feed.following, "when everything fits after collapsing, there is nothing to jump to")

        feed.transcript = "R"
        feed.rows = rows("R", 40)
        spin(0.6)
        expectBottom(scroll, "a fresh transcript shows the live edge")
        let before = renders["R35", default: 0]
        move(scroll, to: offset(scroll) - 150)
        spin(0.3)
        expect(!feed.following, "a reader who scrolled up a little is not following")
        expect(renders["R35", default: 0] == before, "leaving the live edge does not redraw rows that stayed on screen, R35 drew \(renders["R35", default: 0] - before) more times")

        feed.transcript = "L"
        feed.rows = rows("L", 60)
        spin(0.6)
        expectBottom(scroll, "a fresh transcript shows the live edge")
        // A lazy stack that re-measures rows can put the offset back within the same layout pass that anchored a resize to the bottom.
        let held = offset(scroll)
        feed.rows += rows("Lg", 3)
        hosting.layoutSubtreeIfNeeded()
        scroll.contentView.scroll(to: NSPoint(x: 0, y: held))
        scroll.reflectScrolledClipView(scroll.contentView)
        hosting.layoutSubtreeIfNeeded()
        spin(0.5)
        feed.rows += rows("Lh", 2)
        spin(0.5)
        expectBottom(scroll, "layout that moves the offset while a resize settles does not stop following")
        expect(feed.following, "layout that moves the offset while a resize settles leaves the reader following")

        feed.transcript = "M"
        feed.rows = rows("M", 600)
        spin(0.6)
        move(scroll, to: 0)
        NotificationCenter.default.post(name: .jumpToLatest, object: nil)
        spin(0.05)
        scroll.contentView.scroll(to: NSPoint(x: 0, y: 3000))
        scroll.reflectScrolledClipView(scroll.contentView)
        spin(1.5)
        expect(!feed.following || abs(gap(scroll)) < 2, "a view at rest away from the bottom is not following, so Jump to latest shows; following=\(feed.following) gap=\(Int(gap(scroll)))")

        feed.usesTail = true
        feed.transcript = "T"
        feed.rows = rows("T", 200)
        spin(0.6)
        expectBottom(scroll, "a transcript with an eager tail opens at the live edge")
        expect(feed.following, "opening with an eager tail keeps following")
        expect(feed.eager == TranscriptTail.rows, "the tail opens with its fixed row count, got \(feed.eager)")

        for index in 0..<60 {
            feed.rows += rows("Ts\(index)-", 1)
            spin(0.05)
        }
        spin(0.5)
        expectBottom(scroll, "rows streamed into an eager tail stay followed")
        expect(feed.following, "streaming into an eager tail keeps following")
        expect(feed.eager < 30, "the tail hands rows above the viewport to the lazy stack while following, got \(feed.eager)")

        move(scroll, to: 0)
        spin(0.3)
        for index in 0..<30 {
            feed.rows += rows("Tr\(index)-", 1)
            spin(0.05)
        }
        spin(0.5)
        expect(offset(scroll) == 0 && !feed.following, "a reader who scrolled up stays put when rows join the tail, got offset \(offset(scroll))")
        expect(feed.eager <= TranscriptTail.rows + TranscriptTail.slack, "rows streamed while away join the lazy stack, at most the newest rows, a batch short of the slack and the row whose move renders next, eager \(feed.eager)")

        jumpLandsOnNewest(scroll, feed, "jump to latest after streaming while away")
        nearTheEnd(scroll, feed)
        returnMidStream(scroll, feed)
        heldSelection(scroll, feed)

        feed.transcript = "U"
        feed.rows = rows("U", 200)
        spin(0.6)
        expectBottom(scroll, "switching transcripts with an eager tail shows the live edge")
        expect(feed.eager == TranscriptTail.rows, "a switch opens a new tail by the rule, got \(feed.eager)")

        feed.transcript = "X"
        feed.rows = rows("X", 200)
        spin(0.6)
        NotificationCenter.default.post(name: .toggleRow, object: "X198")
        spin(0.4)
        expect(shownOpen["X198"] == true, "a row in the tail expands")
        NotificationCenter.default.post(name: .jumpToLatest, object: nil)
        spin(0.8)
        for index in 0..<40 {
            feed.rows += rows("Xs\(index)-", 1)
            spin(0.05)
        }
        spin(0.5)
        expectBottom(scroll, "streaming past an expanded row stays followed")
        expect(feed.eager < 30, "the expanded row has left the tail, tail is \(feed.eager) rows")
        shownOpen["X198"] = nil
        while shownOpen["X198"] == nil, offset(scroll) > 0 { move(scroll, to: max(offset(scroll) - 300, 0)) }
        expect(shownOpen["X198"] != false, "a row that moved from the tail to the lazy stack stays expanded")

        feed.transcript = "Y"
        feed.rows = rows("Y", 100)
        spin(0.6)
        let streaming = feed.rows.count - 1
        let appearsBefore = appears[feed.rows[streaming].id, default: 0]
        for growth in 1...30 {
            feed.rows[streaming] = Row(id: feed.rows[streaming].id, lines: growth)
            spin(0.03)
        }
        spin(0.4)
        expectBottom(scroll, "a row still streaming in the tail stays followed")
        feed.rows += rows("Yn", 1)
        spin(0.4)
        expectBottom(scroll, "a new row under one that finished streaming stays followed")
        expect(appears[feed.rows[streaming].id, default: 0] == appearsBefore, "a row on screen does not move between stacks while it streams")

        feed.transcript = "Z"
        feed.rows = rows("Z", 100)
        spin(0.6)
        NotificationCenter.default.post(name: .toggleRow, object: "Z98")
        spin(0.5)
        let heldOffset = offset(scroll)
        let heldGap = gap(scroll)
        expect(!feed.following, "expanding a tail row holds the place")
        for page in 0..<6 {
            feed.rows = rows("Zo\(page)-", 20) + feed.rows
            spin(0.8)
            expect(abs(gap(scroll) - heldGap) < 5, "older pages keep a place held in the tail and the drift does not build up, after page \(page) distance from bottom \(heldGap) became \(gap(scroll)), offset \(heldOffset) became \(offset(scroll))")
        }

        for start in [0, 300] as [CGFloat] {
            feed.transcript = "Zt\(Int(start))"
            feed.rows = rows("Zt", 100)
            spin(0.6)
            move(scroll, to: start)
            let place = gap(scroll)
            for page in 0..<6 {
                feed.rows = rows("Zt\(Int(start))p\(page)-", 20) + feed.rows
                expectEventually(abs(gap(scroll) - place) < 2, "older pages loaded in a row keep a reader near the top in place, from \(Int(start)) after page \(page) distance from bottom \(place) became \(gap(scroll))")
                spin(0.3)
                expect(abs(gap(scroll) - place) < 2, "a reader near the top stays in place once page \(page) settles, from \(Int(start)) distance from bottom \(place) became \(gap(scroll))")
            }
        }

        feed.transcript = "Zs"
        feed.rows = rows("Zs", 5)
        spin(0.5)
        for page in 0..<6 {
            feed.rows = rows("Zs\(page)-", 20) + feed.rows
            spin(0.8)
            expectBottom(scroll, "a short transcript that is all tail stays at the live edge as older pages load, after page \(page)")
        }
        expect(feed.following, "a short transcript filling with older pages keeps following")

        feed.transcript = "Zc"
        feed.rows = rows("Zc", 100)
        spin(0.6)
        expectBottom(scroll, "a transcript opened before expanding a row starts at the live edge")
        NotificationCenter.default.post(name: .toggleRow, object: "Zc98")
        spin(0.5)
        NotificationCenter.default.post(name: .collapseRow, object: "Zc98")
        spin(1.0)
        let revealedTop = rowTops["Zc98"] ?? -1
        expect(revealedTop >= offset(scroll) && revealedTop < scroll.contentView.bounds.maxY && gap(scroll) >= -2, "collapsing a tail row keeps its top on screen and the view inside the content, offset \(offset(scroll)) row top \(revealedTop) gap \(gap(scroll))")

        feed.transcript = "Zh"
        feed.rows = rows("Zc", 100)
        spin(0.6)
        expectBottom(scroll, "a transcript opened before holding an older page's place starts at the live edge")
        NotificationCenter.default.post(name: .toggleRow, object: "Zc98")
        spin(0.5)
        feed.rows = rows("Zh", 20) + feed.rows
        spin(0.3)
        NotificationCenter.default.post(name: .collapseRow, object: "Zc98")
        spin(1.0)
        let heldReveal = rowTops["Zc98"] ?? -1
        expect(heldReveal >= offset(scroll) && heldReveal < scroll.contentView.bounds.maxY, "collapsing a row while an older page's place is held keeps the row's top on screen, offset \(offset(scroll)) row top \(heldReveal)")

        feed.transcript = "F"
        feed.rows = rows("F", 1_500)
        spin(0.6)
        move(scroll, to: 0)
        feed.rows += rows("Fn", 5)
        spin(0.3)
        expect(!feed.following, "a reader at the top of a long transcript is not following")
        NotificationCenter.default.post(name: .jumpToLatest, object: nil)
        spin(1.2)
        expectBottom(scroll, "jump to latest from far up reaches the bottom of the tail")
        expect(feed.following, "jump to latest from far up resumes following")

        feed.transcript = "W"
        feed.rows = rows("W", 100) + [Row(id: "Wtall", lines: 400)]
        spin(0.8)
        expectBottom(scroll, "a transcript opens at the end of a tall newest row")
        feed.rows[feed.rows.count - 1] = Row(id: "Wtall", lines: 600)
        spin(0.6)
        expectBottom(scroll, "a tall newest row that grows stays followed")
        expect(feed.following, "a tall newest row keeps following")

        feed.usesTail = false
        feed.header = 80
        feed.transcript = "H"
        feed.rows = rows("H", 60)
        spin(0.6)
        expectBottom(scroll, "a transcript under a tall header opens at the live edge")
        let bottom = offset(scroll)
        move(scroll, to: bottom - 400)
        expect(!feed.following, "scrolling up under a tall header stops following")
        move(scroll, to: bottom)
        expect(feed.following, "scrolling back to the end under a tall header follows again, \(Int(gap(scroll))) points short of the bottom")
        for index in 0..<10 {
            feed.rows += rows("Hs\(index)-", 1)
            spin(0.1)
        }
        expectBottom(scroll, "rows streamed under a tall header stay followed")

        feed.header = 44
        feed.transcript = "S"
        feed.rows = rows("S", 60)
        spin(0.6)
        for index in 0..<10 {
            feed.rows += rows("Sa\(index)-", 1)
            spin(0.05)
        }
        move(scroll, to: offset(scroll) - 400)
        let readerPlace = offset(scroll)
        for index in 0..<20 {
            feed.rows += rows("Sb\(index)-", 1)
            spin(0.05)
        }
        spin(0.6)
        expect(!feed.following && abs(offset(scroll) - readerPlace) < 1, "a reader who scrolls up while rows stream stays put, offset \(readerPlace) became \(offset(scroll))")

        feed.transcript = "N"
        feed.rows = rows("N", 60)
        spin(0.6)
        expectBottom(scroll, "a fresh transcript before a short scroll up opens at the live edge")
        drag(scroll, to: offset(scroll) - 40)
        let nearPlace = offset(scroll)
        expect(!feed.following, "a reader who scrolls to just short of the end stops following")
        for index in 0..<10 { feed.rows += rows("Ns\(index)-", 1); spin(0.05) }
        spin(0.6)
        expect(!feed.following && abs(offset(scroll) - nearPlace) < 1, "a reader who stops just short of the end keeps that place as rows stream, offset \(nearPlace) became \(offset(scroll))")
        drag(scroll, to: bottomOffset(scroll))
        expect(feed.following, "a reader who scrolls back to the end follows again, \(Int(gap(scroll))) points short")

        for top in [0, 44, 69, 70, 100, 120] as [CGFloat] {
            for bottom in [0, 20] as [CGFloat] {
                let size = "header \(Int(top)), bottom inset \(Int(bottom))"
                feed.header = top
                feed.inset = bottom
                feed.transcript = "I\(Int(top))-\(Int(bottom))"
                feed.rows = rows("I\(Int(top))-\(Int(bottom))-", 60)
                spin(0.6)
                expectBottom(scroll, "with \(size) a transcript opens at the live edge")
                let end = offset(scroll)
                move(scroll, to: end - 400)
                expect(!feed.following, "with \(size) scrolling up stops following")
                move(scroll, to: end)
                expect(feed.following, "with \(size) the exact end follows again, \(Int(gap(scroll))) points short")
                move(scroll, to: end - TranscriptFollow<String>.bottomSlack + 1)
                expect(feed.following, "with \(size) a layout move inside the slack keeps following")
                expectBottom(scroll, "with \(size) a followed transcript moved inside the slack returns to the end")
                // SwiftUI undoes a clip move made before it finishes its own scroll.
                spin(0.3)
                move(scroll, to: end - TranscriptFollow<String>.bottomSlack - 1)
                expect(!feed.following, "with \(size) a place just past the slack does not follow, \(Int(gap(scroll))) points short; \(geometry(scroll))")
            }
        }
        feed.header = 44
        feed.inset = 100
    }

    @MainActor
    private static func scrollView(in view: NSView) -> NSScrollView? {
        if let scroll = view as? NSScrollView { return scroll }
        return view.subviews.lazy.compactMap { scrollView(in: $0) }.first
    }

    @MainActor
    private static func jumpLandsOnNewest(_ scroll: NSScrollView, _ feed: Feed, _ message: String) {
        NotificationCenter.default.post(name: .jumpToLatest, object: nil)
        spin(1.0)
        expectBottom(scroll, message)
        guard let newest = feed.rows.last?.id, let top = rowTops[newest] else { return expect(false, "\(message): the newest row was laid out") }
        let clip = scroll.contentView.bounds
        expect(top >= clip.minY && top < clip.maxY, "\(message): the newest row is in view, top \(Int(top)) in \(Int(clip.minY))..<\(Int(clip.maxY))")
    }

    @MainActor
    private static func nearTheEnd(_ scroll: NSScrollView, _ feed: Feed) {
        let height = scroll.contentView.bounds.height
        spin(0.6)
        for _ in 0..<5 where feed.following {
            let end = (scroll.documentView?.frame.height ?? 0) + scroll.contentView.contentInsets.bottom - height
            move(scroll, to: max(end - height, 0))
            spin(0.4)
        }
        expect(!feed.following, "a reader one screen above the end is not following, \(geometry(scroll))")
        func shown() -> [String] {
            feed.rows.filter { live.contains($0.id) && (rowTops[$0.id] ?? -1) >= offset(scroll) && (rowTops[$0.id] ?? .infinity) < offset(scroll) + height - 60 }.map(\.id)
        }
        for _ in 0..<10 where shown().count < 2 { spin(0.2) }
        let inView = shown()
        guard let anchor = inView.first, let opened = inView.dropFirst().first else {
            let near = feed.rows.compactMap { row in rowTops[row.id].map { (row.id, Int($0), live.contains(row.id)) } }.filter { abs(CGFloat($0.1) - offset(scroll)) < 1500 }
            let place = offset(scroll)
            move(scroll, to: place + 1)
            move(scroll, to: place)
            spin(0.5)
            let nudged = shown()
            return expect(false, "rows are in view one screen above the end, \(geometry(scroll)), live \(live.count), tops near \(near), after a 1 pt nudge \(nudged)")
        }
        NotificationCenter.default.post(name: .toggleRow, object: opened)
        spin(0.4)
        expect(shownOpen[opened] == true, "a row in view expands before the stream")
        let placed = (rowTops[anchor] ?? 0) - offset(scroll)
        feed.crossed = 0
        for index in 0..<60 {
            feed.rows += rows("Tn\(index)-", 1)
            spin(0.05)
        }
        spin(0.5)
        expect(feed.crossed > 0, "rows out of view moved into the lazy stack while away")
        expect(abs((rowTops[anchor] ?? 0) - offset(scroll) - placed) < 1, "a row in view keeps its place when it moves to the lazy stack, moved \(Int((rowTops[anchor] ?? 0) - offset(scroll) - placed))")
        expect(shownOpen[opened] == true, "an expanded row stays expanded after it moves to the lazy stack")
        expect(feed.eager <= TranscriptTail.rows + TranscriptTail.slack, "near the end, 60 rows keep the eager tail bounded, got \(feed.eager)")

        let step: CGFloat = 40
        var jumps: [String] = []
        while gap(scroll) > step {
            let before = offset(scroll)
            let document = scroll.documentView?.frame.height ?? 0
            let tracked = feed.rows.first { (rowTops[$0.id] ?? -1) > before + step && (rowTops[$0.id] ?? .infinity) < before + height }?.id
            let seen = tracked.flatMap { rowTops[$0] }.map { $0 - before }
            move(scroll, to: before + step)
            if let tracked, let seen, let now = rowTops[tracked] {
                let shift = now - offset(scroll) - (seen - step)
                if abs(shift) > 1 { jumps.append("\(tracked) shifted \(Int(shift)) at offset \(Int(before))->\(Int(offset(scroll))), document \(Int(document))->\(Int(scroll.documentView?.frame.height ?? 0))") }
            }
        }
        expect(jumps.isEmpty, "scrolling down by hand through rows that streamed in while away never jumps: \(jumps)")
        let clip = scroll.contentView
        move(scroll, to: (scroll.documentView?.frame.height ?? 0) + clip.contentInsets.bottom - clip.bounds.height)
        expectBottom(scroll, "the newest row is reachable by hand")
        NotificationCenter.default.post(name: .collapseRow, object: opened)
        spin(0.3)
    }

    @MainActor
    private static func returnMidStream(_ scroll: NSScrollView, _ feed: Feed) {
        move(scroll, to: max(offset(scroll) - 3 * scroll.contentView.bounds.height, 0))
        for index in 0..<20 {
            feed.rows += rows("Tm\(index)-", 1)
            spin(0.05)
        }
        jumpLandsOnNewest(scroll, feed, "jump to latest mid-stream")
        for index in 0..<30 {
            feed.rows += rows("Tq\(index)-", 1)
            spin(0.05)
        }
        spin(0.5)
        expectBottom(scroll, "streaming on after returning stays followed")
        expect(feed.following, "following resumes after jump to latest")
        expect(feed.eager < 30, "after returning, the tail is the end's rows, eager \(feed.eager)")
    }

    @MainActor
    private static func heldSelection(_ scroll: NSScrollView, _ feed: Feed) {
        move(scroll, to: offset(scroll) - 150)
        spin(0.4)
        expect(!feed.following, "a reader just above the end is not following")
        let height = scroll.contentView.bounds.height
        guard let anchor = feed.rows.dropFirst(feed.rows.count - feed.eager).first(where: { live.contains($0.id) && (rowTops[$0.id] ?? -1) >= offset(scroll) && (rowTops[$0.id] ?? .infinity) < offset(scroll) + height - 60 })?.id
        else { return expect(false, "an eager row is in view just above the end, eager \(feed.eager) \(geometry(scroll))") }
        NotificationCenter.default.post(name: .toggleRow, object: anchor)
        spin(0.4)
        expect(shownOpen[anchor] == true, "an eager row in view expands")
        let placed = (rowTops[anchor] ?? 0) - offset(scroll)
        feed.crossed = 0
        let top = rowTops[anchor] ?? 0
        let selection = SelectedText(frame: NSRect(x: 0, y: top + 1, width: 10, height: 10))
        feed.hold.anchor?.addSubview(selection)
        expect(feed.hold.held(over: [CGRect(x: 0, y: top, width: 600, height: 20)], buttons: 0), "a selection in a row that would move holds the tail")
        expect(!feed.hold.held(over: [CGRect(x: 0, y: top - 400, width: 600, height: 300)], buttons: 0), "a selection outside the rows that would move does not hold the tail")
        for index in 0..<40 {
            feed.rows += rows("Th\(index)-", 1)
            spin(0.05)
        }
        spin(0.3)
        let index = feed.rows.firstIndex { $0.id == anchor } ?? 0
        expect(index >= feed.rows.count - feed.eager, "no row in view crosses stacks while the reader holds a selection or a drag, crossed \(feed.crossed), eager \(feed.eager)")
        expect(abs((rowTops[anchor] ?? 0) - offset(scroll) - placed) < 1, "a held row keeps its place while the stream grows below it")
        expect(feed.eager >= 40, "a held tail keeps streamed rows eager, got \(feed.eager)")
        selection.removeFromSuperview()
        for index in 0..<2 {
            feed.rows += rows("Ti\(index)-", 1)
            spin(0.3)
        }
        expect(feed.crossed >= TranscriptTail.stranded, "once released, the stranded rows in view move into the lazy stack, crossed \(feed.crossed)")
        expect(feed.eager < TranscriptTail.rows + TranscriptTail.slack, "the released tail is bounded, got \(feed.eager)")
        expect(abs((rowTops[anchor] ?? 0) - offset(scroll) - placed) < 1, "a row in view keeps its place when it crosses into the lazy stack, moved \(Int((rowTops[anchor] ?? 0) - offset(scroll) - placed))")
        expect(shownOpen[anchor] == true, "an expanded row stays expanded when it crosses into the lazy stack")
        NotificationCenter.default.post(name: .collapseRow, object: anchor)
        spin(0.3)
        jumpLandsOnNewest(scroll, feed, "jump to latest after a held stream")
    }

    @MainActor
    private static func spin(_ seconds: Double) {
        RunLoop.main.run(until: Date().addingTimeInterval(seconds))
    }

    @MainActor
    private static func move(_ scroll: NSScrollView, to y: CGFloat) {
        scroll.contentView.scroll(to: NSPoint(x: 0, y: y))
        scroll.reflectScrolledClipView(scroll.contentView)
        spin(0.3)
    }

    // A live scroll, as a trackpad or wheel gesture reports it, so the transcript sees a reader's scroll phases.
    @MainActor
    private static func drag(_ scroll: NSScrollView, to y: CGFloat) {
        NotificationCenter.default.post(name: NSScrollView.willStartLiveScrollNotification, object: scroll)
        spin(0.05)
        scroll.contentView.scroll(to: NSPoint(x: 0, y: y))
        scroll.reflectScrolledClipView(scroll.contentView)
        NotificationCenter.default.post(name: NSScrollView.didLiveScrollNotification, object: scroll)
        spin(0.05)
        NotificationCenter.default.post(name: NSScrollView.didEndLiveScrollNotification, object: scroll)
        spin(0.3)
    }

    @MainActor
    private static func bottomOffset(_ scroll: NSScrollView) -> CGFloat {
        offset(scroll) + gap(scroll)
    }

    @MainActor
    private static func offset(_ scroll: NSScrollView) -> CGFloat {
        scroll.contentView.bounds.origin.y
    }

    @MainActor
    private static func gap(_ scroll: NSScrollView) -> CGFloat {
        let clip = scroll.contentView
        return (scroll.documentView?.frame.height ?? 0) + clip.contentInsets.bottom - clip.bounds.maxY
    }

    @MainActor
    private static func expectBottom(_ scroll: NSScrollView, _ message: String) {
        expectEventually(abs(gap(scroll)) < 2, "\(message), \(Int(gap(scroll))) points short of the bottom at offset \(Int(offset(scroll))); \(geometry(scroll))")
    }

    @MainActor
    private static func geometry(_ scroll: NSScrollView) -> String {
        let clip = scroll.contentView
        let ids = watched?.rows.suffix(2).map(\.id) ?? []
        let pitch = ids.count == 2 ? (rowTops[ids[1]] ?? 0) - (rowTops[ids[0]] ?? 0) : 0
        return "document \(scroll.documentView?.frame.height ?? 0), clip \(clip.bounds), insets top \(clip.contentInsets.top) bottom \(clip.contentInsets.bottom), following \(watched?.following ?? false), last row pitch \(pitch); SwiftUI geometry, newest last at \(String(format: "%.3f", Date().timeIntervalSince1970.truncatingRemainder(dividingBy: 100))):\n  " + seenGeometry.joined(separator: "\n  ")
    }

    @MainActor
    private static func expectEventually(_ condition: @autoclosure () -> Bool, _ message: @autoclosure () -> String) {
        let deadline = Date().addingTimeInterval(2)
        while !condition(), Date() < deadline { spin(0.02) }
        expect(condition(), message())
    }

    private static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
