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
        print("peer envelope tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
