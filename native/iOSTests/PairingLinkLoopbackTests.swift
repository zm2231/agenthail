import XCTest
@testable import Agenthail

final class PairingLinkLoopbackTests: XCTestCase {
    private func link(_ endpoint: String) -> URL {
        URL(string: "agenthail://pair?endpoint=\(endpoint)&secret=12345678901234567890&host=live-a&name=live-a")!
    }

    func testALoopbackLinkNeedsTheLiveTestLaunchArgument() throws {
        let loopback = link("http%3A%2F%2F127.0.0.1%3A50123")
        XCTAssertThrowsError(try PairingLink(url: loopback, arguments: []))
        XCTAssertThrowsError(try PairingLink(url: loopback, arguments: ["--live-test-loopbackx"]))
        let live = try PairingLink(url: loopback, arguments: [PairingLink.loopbackArgument])
        XCTAssertEqual(live.endpoint.absoluteString, "http://127.0.0.1:50123")
        XCTAssertEqual(live.hostID, "live-a")
    }

    func testOnlyLoopbackPairingsAreLiveTestHostsAndOnlyWithTheArgument() throws {
        KeychainStore.removeAll()
        defer { KeychainStore.removeAll() }
        for (hostID, endpoint) in [("live-a", "http://127.0.0.1:50123"), ("studio", "https://studio.tailnet.ts.net")] {
            try PairedCredential.save(PairedHostCredential(hostID: hostID, hostName: hostID, endpoint: URL(string: endpoint)!, token: "token", pairedAt: Date()))
        }
        XCTAssertEqual(LiveTestHosts.loopbackHostIDs(arguments: [PairingLink.loopbackArgument]), ["live-a"])
        XCTAssertEqual(LiveTestHosts.loopbackHostIDs(arguments: []), [])
    }

    func testTheLaunchArgumentOpensOnlyHTTPOnThisMachinesLoopbackWithAPort() {
        for endpoint in ["http%3A%2F%2Flocalhost%3A50123", "http%3A%2F%2F127.0.0.1", "http%3A%2F%2F10.0.0.2%3A50123", "http%3A%2F%2Fmac.tailnet.ts.net%3A7412", "ftp%3A%2F%2F127.0.0.1%3A50123", "http%3A%2F%2F127.0.0.1%3A50123%2Fapi"] {
            XCTAssertThrowsError(try PairingLink(url: link(endpoint), arguments: [PairingLink.loopbackArgument]), endpoint)
        }
    }
}
