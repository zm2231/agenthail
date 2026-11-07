@testable import AgenthailHelpers
import Foundation
import SwiftUI

@main
struct TranscriptJumpTest {
    @MainActor
    static func main() {
        var position = ScrollPosition(edge: .bottom)
        var following = false
        let positionBinding = Binding(get: { position }, set: { position = $0 })
        let followingBinding = Binding(get: { following }, set: { following = $0 })

        scrollToLatest(positionBinding, following: followingBinding, latest: "r9")
        expect(following, "jumping to latest follows")
        expect(position.viewID(type: String.self) == "r9", "the jump targets the newest row first")
        spin()
        expect(position.edge == .bottom && position.viewID(type: String.self) == nil, "the jump then reaches the edge, below rows that follow the newest block")

        scrollToLatest(positionBinding, following: followingBinding, latest: "r9")
        position.scrollTo(point: CGPoint(x: 0, y: 120))
        spin()
        expect(position.point == CGPoint(x: 0, y: 120) && position.edge == nil, "a scroll that moved the position before the edge step, such as a session switch, is kept")

        scrollToLatest(positionBinding, following: followingBinding, latest: nil)
        expect(position.edge == .bottom, "with no rows the jump goes straight to the edge")
    }

    @MainActor
    static func spin() {
        RunLoop.main.run(until: Date().addingTimeInterval(0.1))
    }

    static func expect(_ condition: Bool, _ message: String) {
        guard condition else {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
