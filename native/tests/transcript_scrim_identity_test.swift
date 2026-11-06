@testable import AgenthailHelpers
import AppKit
import SwiftUI

@MainActor
final class Appearances {
    var content = 0
    var rows = 0
}

@MainActor
final class ScrimSwitch: ObservableObject {
    @Published var scrim: TranscriptScrim?
}

struct IdentityTree: View {
    @ObservedObject var toggle: ScrimSwitch
    let seen: Appearances
    let anchor: ScrimAnchor

    var body: some View {
        ScrollView {
            VStack(spacing: 20) {
                ForEach(0..<3, id: \.self) { index in
                    Text("Message \(index)").frame(height: 40).onAppear { seen.rows += 1 }.transcriptScrimRow()
                }
            }
            .onAppear { seen.content += 1 }
            .frame(maxWidth: .infinity)
            .coordinateSpace(.named(TranscriptRows.contentSpace))
            .transcriptScrimContent()
            .transcriptScrimOrigin()
        }
        .environment(\.transcriptScrim, toggle.scrim)
        .environment(\.backdropPlace, BackdropPlace(role: nil, anchor: anchor))
    }
}

@main
struct TranscriptScrimIdentityTest {
    @MainActor
    static func main() {
        _ = NSApplication.shared
        var failures = 0
        func check(_ condition: Bool, _ message: String) {
            if !condition {
                print("FAIL: \(message)")
                failures += 1
            }
        }

        let toggle = ScrimSwitch()
        let seen = Appearances()
        let anchor = ScrimAnchor()
        let hosting = NSHostingView(rootView: IdentityTree(toggle: toggle, seen: seen, anchor: anchor))
        let window = NSWindow(contentRect: CGRect(x: -20000, y: -20000, width: 400, height: 300), styleMask: [.borderless], backing: .buffered, defer: false)
        window.contentView = hosting
        func settle() {
            for _ in 0..<10 {
                RunLoop.main.run(until: Date().addingTimeInterval(0.02))
                hosting.layoutSubtreeIfNeeded()
            }
        }
        settle()
        let content = seen.content, rows = seen.rows
        check(content == 1 && rows == 3, "the transcript and its rows appear once, got content \(content) rows \(rows)")

        let scrim = anchor.transcript
        toggle.scrim = scrim
        settle()
        check(seen.content == content && seen.rows == rows, "turning the scrim on keeps the transcript and its rows, got content \(seen.content) rows \(seen.rows)")
        check(scrim.rowLayers.count == 3, "the rows report to the scrim once it is on, got \(scrim.rowLayers.count)")
        check(scrim.origin != nil, "the transcript reports its origin once the scrim is on")

        toggle.scrim = nil
        settle()
        check(seen.content == content && seen.rows == rows, "turning the scrim off keeps the transcript and its rows, got content \(seen.content) rows \(seen.rows)")

        if failures > 0 { exit(1) }
        print("transcript scrim identity: the scrim turning on or off leaves the transcript and its rows in place")
    }
}
