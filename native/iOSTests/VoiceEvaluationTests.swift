#if DEBUG && targetEnvironment(simulator)
import AVFAudio
import XCTest
@testable import Agenthail

final class VoiceEvaluationTests: XCTestCase {
    func testRecordingIsConvertedToFortyEightKilohertzMonoSamples() throws {
        let url = FileManager.default.temporaryDirectory.appendingPathComponent("voice-evaluation-\(UUID().uuidString).wav")
        defer { try? FileManager.default.removeItem(at: url) }
        let tone = (0..<16_000).map { Int16(sin(Double($0) * 2 * .pi * 440 / 16_000) * 16_000) }
        try wav(tone, sampleRate: 16_000).write(to: url)

        let samples = try VoiceEvaluation.samples(url)
        XCTAssertEqual(Double(samples.count), 48_000, accuracy: 480)
        XCTAssertGreaterThan(samples.map { abs(Int($0)) }.max() ?? 0, 10_000)
    }

    private func wav(_ samples: [Int16], sampleRate: UInt32) -> Data {
        var data = Data()
        func append<T: FixedWidthInteger>(_ value: T) { withUnsafeBytes(of: value.littleEndian) { data.append(contentsOf: $0) } }
        let bytes = UInt32(samples.count * 2)
        data.append(contentsOf: Array("RIFF".utf8)); append(36 + bytes); data.append(contentsOf: Array("WAVEfmt ".utf8))
        append(UInt32(16)); append(UInt16(1)); append(UInt16(1)); append(sampleRate); append(sampleRate * 2); append(UInt16(2)); append(UInt16(16))
        data.append(contentsOf: Array("data".utf8)); append(bytes)
        samples.forEach { append($0) }
        return data
    }

    func testMissingRecordingRefusesTheCall() {
        XCTAssertThrowsError(try VoiceEvaluation.samples(URL(fileURLWithPath: "/nonexistent/request-1.wav")))
    }
}
#endif
