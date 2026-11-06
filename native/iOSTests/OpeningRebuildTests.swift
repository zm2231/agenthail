import XCTest
@testable import Agenthail

@MainActor
final class OpeningRebuildTests: XCTestCase {
    private func drain() async {
        for _ in 0..<5 { await Task.yield() }
    }

    func testOneOpenRebuildsOnceWhicheverComesFirst() async {
        for rowFirst in [true, false] {
            let opening = OpeningRebuild()
            var rebuilds = 0
            let open = opening.open(following: true)
            if rowFirst { opening.rowAppeared("b") }
            opening.land(open)
            opening.fire(latest: "b") { rebuilds += 1 }
            if !rowFirst { opening.rowAppeared("b") }
            opening.fire(latest: "b") { rebuilds += 1 }
            opening.fire(latest: "b") { rebuilds += 1 }
            await drain()
            XCTAssertEqual(rebuilds, 1, "row first: \(rowFirst)")
        }
    }

    func testAnOpenThatWasLeftCannotRebuildTheNextSession() async {
        let opening = OpeningRebuild()
        var rebuilt: [String] = []
        let first = opening.open(following: true)
        _ = opening.open(following: true)
        opening.land(first)
        opening.rowAppeared("b")
        opening.fire(latest: "b") { rebuilt.append("early") }
        await drain()
        XCTAssertEqual(rebuilt, [], "the left open landed the next session before it loaded")
    }

    func testARebuildQueuedBeforeASwitchIsDropped() async {
        let opening = OpeningRebuild()
        var rebuilt: [String] = []
        let first = opening.open(following: true)
        opening.rowAppeared("a")
        opening.land(first)
        opening.fire(latest: "a") { rebuilt.append("a") }
        let second = opening.open(following: true)
        await drain()
        XCTAssertEqual(rebuilt, [])
        opening.rowAppeared("b")
        opening.land(second)
        opening.fire(latest: "b") { rebuilt.append("b") }
        await drain()
        XCTAssertEqual(rebuilt, ["b"])
    }

    func testNotFollowingNeverRebuilds() async {
        let opening = OpeningRebuild()
        var rebuilds = 0
        let open = opening.open(following: false)
        opening.rowAppeared("b")
        opening.land(open)
        opening.fire(latest: "b") { rebuilds += 1 }
        await drain()
        XCTAssertEqual(rebuilds, 0)
    }

    func testACancelledCallerLeavesTheLoadForTheNextOne() async {
        let loads = SharedLoads<Data?>()
        var fetches = 0
        let gate = AsyncStream<Void>.makeStream()
        let first = Task { @MainActor in
            await loads.value(for: "k") {
                fetches += 1
                for await _ in gate.stream { break }
                return Data("image".utf8)
            }
        }
        await drain()
        first.cancel()
        let second = Task { @MainActor in await loads.value(for: "k") { fetches += 1; return nil } }
        await drain()
        gate.continuation.yield()
        let value = await second.value
        _ = await first.value
        XCTAssertEqual(fetches, 1)
        XCTAssertEqual(value, Data("image".utf8))
    }
}
