import Foundation

@main
struct PeerEnvelopeTest {
    static func main() {
        let raw = "[Claude peer message from uds:/tmp/cc-socks/10906.sock]\n<cross-session-message from=\"uds:/tmp/cc-socks/10906.sock\" from-name=\"agenthail-collab\" from-mode=\"bypass\">\nThree more items.\n9. Roles.\n</cross-session-message>"
        let parsed = PeerEnvelope(raw)
        expect(parsed.sender == "agenthail-collab", "sender comes from from-name")
        expect(parsed.body == "Three more items.\n9. Roles.", "body excludes the envelope")
        let plain = PeerEnvelope("Run the tests")
        expect(plain.sender == nil && plain.body == "Run the tests", "plain text is unchanged")
        for malformed in [
            "<cross-session-message from=\"a\">",
            "<cross-session-message from=\"a\">hello",
            "<cross-session-message>hello</cross-session-message>",
            "[Claude peer message from x] just text",
        ] {
            let result = PeerEnvelope(malformed)
            expect(result.sender == nil && result.body == malformed, "malformed envelope is preserved: \(malformed)")
        }
        print("peer envelope tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
