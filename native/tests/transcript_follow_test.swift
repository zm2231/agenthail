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
    @Published var holdBottom = false
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
                    Text(String(repeating: "transcript line\n", count: row.lines) + row.id)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .padding(24)
        }
        .transcriptFollow(feed.transcript, holdBottom: feed.holdBottom, position: $position, following: $following)
        .safeAreaInset(edge: .bottom, spacing: 0) { Color.gray.frame(height: feed.inset) }
        .onChange(of: following) { feed.following = following }
        .onReceive(NotificationCenter.default.publisher(for: .jumpToLatest)) { _ in
            scrollToLatest($position, following: $following)
        }
    }
}

extension Notification.Name {
    static let jumpToLatest = Notification.Name("jumpToLatest")
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
        feed.holdBottom = true
        spin(0.1)
        let fromBottom = gap(scroll)
        feed.rows = rows("Co", 10) + feed.rows
        spin(0.5)
        feed.holdBottom = false
        expect(abs(gap(scroll) - fromBottom) < 2, "loading earlier rows keeps the reader's place, distance from bottom \(fromBottom) became \(gap(scroll))")

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
            feed.holdBottom = true
            spin(0.05)
            feed.rows = rows("Gp\(page)-", 20) + feed.rows
            spin(0.35)
            feed.holdBottom = false
        }
        spin(0.4)
        expectBottom(scroll, "a short transcript that fills the viewport with earlier pages stays at the live edge")

        for index in 0..<5 {
            feed.rows += rows("Es\(index)-", 1)
            spin(0.15)
        }
        spin(0.4)
        expectBottom(scroll, "a following reader sees streamed rows")
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
        expect(abs(gap(scroll)) < 2, "\(message), \(Int(gap(scroll))) points short of the bottom at offset \(Int(offset(scroll)))")
    }

    private static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
