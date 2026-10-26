import XCTest
@testable import Agenthail

@MainActor
final class StreamBatchTests: XCTestCase {
    private final class Drains {
        var batches: [[Int]] = []
    }

    func testRowsThatArriveBeforeTheMainActorRunsAreAppliedTogether() async throws {
        let drains = Drains()
        let batch = StreamBatch<Int> { drains.batches.append($0) }
        for row in 1...150 { batch.add(row) }
        try await waitUntil { !drains.batches.isEmpty }
        XCTAssertEqual(drains.batches, [Array(1...150)])
    }

    func testARowAfterADrainStartsTheNextBatch() async throws {
        let drains = Drains()
        let batch = StreamBatch<Int> { drains.batches.append($0) }
        batch.add(1)
        try await waitUntil { drains.batches.count == 1 }
        batch.add(2)
        batch.add(3)
        try await waitUntil { drains.batches.count == 2 }
        XCTAssertEqual(drains.batches, [[1], [2, 3]])
    }

    func testFlushAppliesWhatIsWaitingAtOnce() {
        let drains = Drains()
        let batch = StreamBatch<Int> { drains.batches.append($0) }
        batch.add(1)
        batch.add(2)
        batch.flush()
        XCTAssertEqual(drains.batches, [[1, 2]])
        batch.flush()
        XCTAssertEqual(drains.batches, [[1, 2]])
    }

    func testOnlyShortSingleLineEventsAreDividers() {
        XCTAssertTrue(TranscriptDivider.fits("gpt-5.6-terra"))
        XCTAssertFalse(TranscriptDivider.fits("first\nsecond"))
        XCTAssertFalse(TranscriptDivider.fits(String(repeating: "x", count: 161)))
    }

    private func waitUntil(_ condition: () -> Bool) async throws {
        for _ in 0..<200 where !condition() { try await Task.sleep(for: .milliseconds(10)) }
        XCTAssertTrue(condition())
    }
}
