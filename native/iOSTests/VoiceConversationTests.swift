import XCTest
@testable import Agenthail

final class VoiceConversationTests: XCTestCase {
    func testPreviousCanonicalCallDoesNotHideCurrentFlatTranscript() throws {
        let json = #"{"protocol":1,"phase":"connected","attemptId":"new","events":[{"sequence":1,"method":"thread/realtime/item/completed","params":{"item":{"id":"old","realtimeSessionId":"old","type":"transcriptSegment","role":"assistant","text":"Old answer"}}},{"sequence":2,"method":"thread/realtime/transcript/done","params":{"role":"assistant","text":"Current answer"}}],"truncated":false,"occupied":false}"#
        let state = try JSONDecoder().decode(VoiceState.self, from: Data(json.utf8))
        XCTAssertEqual(state.transcripts.map(\.text), ["Current answer"])
    }
    func testProviderInputDuringMuteIsNotLabeledAsUserSpeech() throws {
        let json = #"{"protocol":1,"phase":"connected","events":[{"sequence":1,"method":"thread/realtime/transcript/done","params":{"role":"user","text":"Unexplained input","inputMuted":true}}],"truncated":false,"occupied":false}"#
        let state = try JSONDecoder().decode(VoiceState.self, from: Data(json.utf8))
        XCTAssertEqual(state.transcripts.first?.label, "Provider transcript · mic muted")
    }
    @MainActor
    func testWorkerContextUsesExplicitCommentaryChannel() throws {
        let bytes = try XCTUnwrap(NativeVoicePeer.contextFrames("Quiet worker update").first)
        let frame = try XCTUnwrap(JSONSerialization.jsonObject(with: bytes) as? [String: Any])
        XCTAssertEqual(frame["type"] as? String, "session.context.append")
        XCTAssertEqual(frame["channel"] as? String, "commentary")
        XCTAssertThrowsError(try NativeVoicePeer.contextFrames(String(repeating: "x", count: 49 * 1024)))
        let text = String(repeating: "界👩🏽‍💻", count: 100)
        let frames = try NativeVoicePeer.contextFrames(text)
        let parts = try frames.map { data -> String in
            let frame = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
            let content = try XCTUnwrap(frame["content"] as? [[String: String]])
            let part = try XCTUnwrap(content.first?["text"])
            XCTAssertLessThanOrEqual(part.utf8.count, 500)
            return part
        }
        XCTAssertEqual(parts.joined(), text)
    }
    func testArbitraryProviderItemDoesNotBreakCallState() throws {
        let json = #"{"protocol":1,"phase":"connected","events":[{"sequence":1,"method":"thread/realtime/itemAdded","params":{"item":{"content":[{"type":"unknown"}]}}}],"truncated":false,"occupied":false}"#
        let state = try JSONDecoder().decode(VoiceState.self, from: Data(json.utf8))
        XCTAssertEqual(state.phase, "connected")
        XCTAssertTrue(state.transcripts.isEmpty)
    }
    func testCanonicalItemsKeepInterleavedResponsesSeparateAndAvoidFlatDuplicates() throws {
        let json = #"""
        {"protocol":1,"phase":"connected","attemptId":"call","truncated":false,"occupied":false,"events":[
        {"sequence":1,"method":"thread/realtime/item/started","params":{"item":{"id":"a","realtimeSessionId":"call","type":"transcriptSegment","role":"assistant","text":""}}},
        {"sequence":2,"method":"thread/realtime/item/started","params":{"item":{"id":"b","realtimeSessionId":"call","type":"transcriptSegment","role":"assistant","text":""}}},
        {"sequence":3,"method":"thread/realtime/item/transcript/delta","params":{"itemId":"a","delta":"First"}},
        {"sequence":4,"method":"thread/realtime/item/transcript/delta","params":{"itemId":"b","delta":"Second"}},
        {"sequence":5,"method":"thread/realtime/transcript/done","params":{"role":"assistant","text":"Must not duplicate"}},
        {"sequence":6,"method":"thread/realtime/item/completed","params":{"item":{"id":"a","realtimeSessionId":"call","type":"transcriptSegment","role":"assistant","text":"First final"}}},
        {"sequence":7,"method":"thread/realtime/item/completed","params":{"item":{"id":"old","realtimeSessionId":"previous","type":"transcriptSegment","role":"user","text":"Old call"}}}
        ]}
        """#
        let state = try JSONDecoder().decode(VoiceState.self, from: Data(json.utf8))
        XCTAssertEqual(state.transcripts.map(\.id), ["a", "b"])
        XCTAssertEqual(state.transcripts.map(\.text), ["First final", "Second"])
    }
}
