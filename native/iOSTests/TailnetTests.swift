import XCTest
@testable import Agenthail

final class TailnetTests: XCTestCase {
    func testATailnetAddressOnAUtunInterfaceIsUp() {
        XCTAssertTrue(Tailnet.isUp([InterfaceAddress(name: "en0", address: "192.168.1.20"), InterfaceAddress(name: "utun4", address: "100.101.102.103")]))
        XCTAssertTrue(Tailnet.isUp([InterfaceAddress(name: "utun2", address: "fd7a:115c:a1e0::1234")]))
    }

    func testACarrierAddressInTheSameRangeWithoutUtunIsOff() {
        XCTAssertFalse(Tailnet.isUp([InterfaceAddress(name: "pdp_ip0", address: "100.72.14.9"), InterfaceAddress(name: "en0", address: "10.0.0.4")]))
    }

    func testSystemUtunInterfacesWithoutATailnetAddressAreOff() {
        XCTAssertFalse(Tailnet.isUp([InterfaceAddress(name: "utun0", address: "fe80::1%utun0"), InterfaceAddress(name: "utun1", address: "fe80::2%utun1")]))
    }

    func testTheTailnetRangesAreExact() {
        XCTAssertTrue(Tailnet.isTailnetAddress("100.64.0.1"))
        XCTAssertTrue(Tailnet.isTailnetAddress("100.127.255.254"))
        XCTAssertFalse(Tailnet.isTailnetAddress("100.63.255.255"))
        XCTAssertFalse(Tailnet.isTailnetAddress("100.128.0.1"))
        XCTAssertFalse(Tailnet.isTailnetAddress("fd7a:115c:a1e1::1"))
        XCTAssertFalse(Tailnet.isTailnetAddress("forge.example.ts.net"))
    }

    func testTailnetEndpointsAreNamedByTsNetOrATailnetAddress() {
        XCTAssertTrue(Tailnet.names(URL(string: "https://forge.example.ts.net:7412")!))
        XCTAssertTrue(Tailnet.names(URL(string: "https://100.70.1.2:7412")!))
        XCTAssertTrue(Tailnet.names(URL(string: "https://[fd7a:115c:a1e0::5]:7412")!))
        XCTAssertFalse(Tailnet.names(URL(string: "http://127.0.0.1:7412")!))
        XCTAssertFalse(Tailnet.names(URL(string: "https://computer.example.com")!))
    }

    func testReasonsSayWhatFailed() {
        XCTAssertEqual(HostProblem(URLError(.notConnectedToInternet))?.message, "This iPhone isn't connected to the internet.")
        XCTAssertEqual(HostProblem(URLError(.timedOut), waited: 8.2)?.message, "Didn't answer within 8 s.")
        XCTAssertEqual(HostProblem(URLError(.cannotFindHost))?.message, "Its address can't be found from this network.")
        XCTAssertEqual(HostProblem(URLError(.networkConnectionLost))?.message, "The connection dropped.")
        XCTAssertNil(HostProblem(AgenthailAPIError.invalidResponse))
    }

    func testTailscaleOffIsSaidOnceForTailnetHosts() {
        let forge = HostProblem.Failing(name: "forge", error: "Didn't answer within 8 s.", problem: .timedOut(8), endpoint: URL(string: "https://forge.example.ts.net")!)
        let mac = HostProblem.Failing(name: "mac", error: "Its address can't be found from this network.", problem: .notFound, endpoint: URL(string: "https://mac.example.ts.net")!)
        XCTAssertEqual(HostProblem.summary([forge, mac], hostCount: 2, tailnetUp: { false }), HostProblem.tailnetOff)
        XCTAssertEqual(HostProblem.summary([forge, mac], hostCount: 2, tailnetUp: { true }), "forge: Didn't answer within 8 s.\nmac: Its address can't be found from this network.")
    }

    func testOtherSetupsKeepTheirOwnReasons() {
        let lan = HostProblem.Failing(name: "studio", error: "Didn't answer within 8 s.", problem: .timedOut(8), endpoint: URL(string: "https://studio.example.com")!)
        XCTAssertEqual(HostProblem.summary([lan], hostCount: 1, tailnetUp: { false }), "Didn't answer within 8 s.")
        let refused = HostProblem.Failing(name: "forge", error: "Update Agenthail on forge to connect.", problem: nil, endpoint: URL(string: "https://forge.example.ts.net")!)
        XCTAssertEqual(HostProblem.summary([refused], hostCount: 1, tailnetUp: { false }), "Update Agenthail on forge to connect.", "a refusal is not a network problem")
    }

    func testNoInternetIsSaidOnce() {
        let a = HostProblem.Failing(name: "a", error: HostProblem.offline.message, problem: .offline, endpoint: URL(string: "https://a.example.ts.net")!)
        let b = HostProblem.Failing(name: "b", error: HostProblem.offline.message, problem: .offline, endpoint: URL(string: "https://b.example.ts.net")!)
        XCTAssertEqual(HostProblem.summary([a, b], hostCount: 2, tailnetUp: { false }), HostProblem.offline.message)
        let slow = HostProblem.Failing(name: "b", error: "Didn't answer within 8 s.", problem: .timedOut(8), endpoint: URL(string: "https://b.example.ts.net")!)
        XCTAssertEqual(HostProblem.summary([a, slow], hostCount: 2, tailnetUp: { false }), HostProblem.offline.message, "no internet is the cause even when another request timed out first")
    }
}
