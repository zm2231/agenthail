import XCTest
@testable import Agenthail

final class VoiceConversationTests: XCTestCase {
    func testProviderRejectionIsNotSilentlyDroppedOrEchoed() {
        let event = NativeVoicePeer.providerEvent(Data(#"{"type":"error","error":{"message":"private provider payload"}}"#.utf8))
        XCTAssertEqual(event?.0, "error")
        XCTAssertFalse(event?.1.contains("private provider payload") ?? true)
    }
    func testTypedInputIsNotAttributedToMutedProviderAudio() throws {
        let json = #"{"protocol":1,"phase":"connected","events":[{"sequence":1,"method":"thread/realtime/transcript/done","params":{"role":"user","text":"Typed request","inputSource":"typed","inputMuted":false}}],"truncated":false,"occupied":false}"#
        let state = try JSONDecoder().decode(VoiceState.self, from: Data(json.utf8))
        XCTAssertEqual(state.transcripts.first?.label, "You · typed")
    }
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
    func testOnlyCorrelatedResultsUseSpeakableChannel() throws {
        let result = VoiceContextFrame(kind: "delegation-result", id: 1, generation: 2, text: "Requested result", delegationId: "request-1", channel: "speakable")
        let bytes = try XCTUnwrap(NativeVoicePeer.resultFrames(result).first)
        let frame = try XCTUnwrap(JSONSerialization.jsonObject(with: bytes) as? [String: Any])
        XCTAssertEqual(frame["type"] as? String, "delegation.context.append")
        XCTAssertEqual(frame["delegation_item_id"] as? String, "request-1")
        XCTAssertEqual(frame["channel"] as? String, "speakable")
        XCTAssertThrowsError(try NativeVoicePeer.resultFrames(VoiceContextFrame(kind: "delegation-result", id: 1, generation: 2, text: "Background", delegationId: "", channel: "commentary")))
        XCTAssertThrowsError(try NativeVoicePeer.resultFrames(VoiceContextFrame(kind: "delegation-result", id: 1, generation: 2, text: String(repeating: "x", count: 17 * 1024), delegationId: "request-1", channel: "speakable")))
        let text = String(repeating: "界👩🏽‍💻", count: 100)
        let frames = try NativeVoicePeer.resultFrames(VoiceContextFrame(kind: "delegation-result", id: 2, generation: 2, text: text, delegationId: "request-1", channel: "speakable"))
        XCTAssertEqual(frames.count, 1)
        let parts = try frames.map { data -> String in
            let frame = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
            let content = try XCTUnwrap(frame["content"] as? [[String: String]])
            let part = try XCTUnwrap(content.first?["text"])
            return part
        }
        XCTAssertEqual(parts.joined(), text)
    }
    @MainActor
    func testTypedResultUsesSessionAppendWithoutInventedDelegation() throws {
        let result = VoiceContextFrame(kind: "typed-result", id: 1, generation: 2, text: "Typed result", delegationId: "", channel: "speakable")
        let bytes = try XCTUnwrap(NativeVoicePeer.resultFrames(result).first)
        let frame = try XCTUnwrap(JSONSerialization.jsonObject(with: bytes) as? [String: Any])
        XCTAssertEqual(frame["type"] as? String, "session.context.append")
        XCTAssertNil(frame["delegation_item_id"])
    }
    @MainActor
    func testInterruptionSuppressesRetiredOutputUntilCurrentAnswer() {
        var states: [Bool] = []
        let playback = VoicePlayback { states.append($0) }
        XCTAssertTrue(playback.canPublish)
        playback.outputAdded()
        XCTAssertFalse(playback.canPublish)
        playback.inputStarted(speechPending: true)
        playback.inputFinished(speechPending: true, hasTranscript: true)
        XCTAssertEqual(states, [true])
        playback.outputFinished()
        playback.outputAdded()
        XCTAssertEqual(states, [true, false])
        playback.outputFinished()
        XCTAssertTrue(playback.canPublish)
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
