@testable import AgenthailHelpers
import AppKit
import SwiftUI

struct Row: Identifiable {
    let id: String
    let lines: Int
}

@MainActor
final class Feed: ObservableObject {
    @Published var transcript = "A"
    @Published var rows: [Row] = []
    @Published var inset: CGFloat = 100
    var following = true
}

func rows(_ prefix: String, _ count: Int) -> [Row] {
    (0..<count).map { Row(id: "\(prefix)\($0)", lines: 1 + ($0 * 7) % 9) }
}

struct Transcript: View {
    @ObservedObject var feed: Feed
    @State private var position = ScrollPosition(edge: .bottom)
    @State private var following = true

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                ForEach(feed.rows) { row in
                    RowView(row: row)
                }
            }
            .padding(24)
        }
        .transcriptFollow(feed.transcript, leadingRow: feed.rows.first?.id, hasRow: { id in feed.rows.contains { $0.id == id } }, position: $position, following: $following)
        .safeAreaInset(edge: .bottom, spacing: 0) { Color.gray.frame(height: feed.inset) }
        .onChange(of: following) { feed.following = following }
        .onReceive(NotificationCenter.default.publisher(for: .jumpToLatest)) { _ in
            scrollToLatest($position, following: $following)
        }
    }
}

struct RowView: View {
    let row: Row
    @State private var expanded = false
    @Environment(\.holdTranscriptPlace) private var holdPlace

    var body: some View {
        Text(String(repeating: "transcript line\n", count: row.lines + (expanded ? 30 : 0)) + row.id)
            .frame(maxWidth: .infinity, alignment: .leading)
            .onReceive(NotificationCenter.default.publisher(for: .toggleRow)) { note in
                guard note.object as? String == row.id else { return }
                holdPlace()
                expanded.toggle()
            }
    }
}

extension Notification.Name {
    static let jumpToLatest = Notification.Name("jumpToLatest")
    static let toggleRow = Notification.Name("toggleRow")
}

@main
struct TranscriptFollowTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        let feed = Feed()
        let hosting = NSHostingView(rootView: Transcript(feed: feed))
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 800, height: 600), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.contentView = hosting
        window.orderFrontRegardless()
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
        spin(0.6)
        expect(offset(scroll) == 500 && !feed.following, "a page that replaces the rows leaves an up-scrolled reader where they were, got offset \(offset(scroll))")

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
    }

    @MainActor
    private static func scrollView(in view: NSView) -> NSScrollView? {
        if let scroll = view as? NSScrollView { return scroll }
        return view.subviews.lazy.compactMap { scrollView(in: $0) }.first
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
        expectEventually(abs(gap(scroll)) < 2, "\(message), \(Int(gap(scroll))) points short of the bottom at offset \(Int(offset(scroll)))")
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
