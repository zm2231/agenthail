import XCTest
@testable import Agenthail

final class PushSetupFailureTests: XCTestCase {
    func testNetworkDropsAndBusyServersAreTransient() {
        for code: URLError.Code in [.networkConnectionLost, .notConnectedToInternet, .timedOut, .cannotConnectToHost, .cannotFindHost, .dnsLookupFailed, .internationalRoamingOff, .dataNotAllowed, .cancelled] {
            XCTAssertEqual(PushSetupFailure.classify(URLError(code)), .transient(retryAfter: nil), "\(code)")
        }
        for status in [429, 500, 502, 503, 504] {
            XCTAssertEqual(PushSetupFailure.classify(AgenthailAPIError.request(status, "busy")), .transient(retryAfter: nil), "\(status)")
        }
        XCTAssertEqual(PushSetupFailure.classify(CancellationError()), .transient(retryAfter: nil))
        XCTAssertEqual(PushSetupFailure.classify(PushRelayBusy(status: 503, retryAfter: 12)), .transient(retryAfter: 12))
    }

    func testRefusalsAndLocalFailuresArePersistent() {
        for status in [400, 403, 404, 409] {
            XCTAssertEqual(PushSetupFailure.classify(AgenthailAPIError.request(status, "no")), .persistent, "\(status)")
        }
        XCTAssertEqual(PushSetupFailure.classify(URLError(.badURL)), .persistent)
        XCTAssertEqual(PushSetupFailure.classify(URLError(.userAuthenticationRequired)), .persistent)
        XCTAssertEqual(PushSetupFailure.classify(DecodingError.dataCorrupted(.init(codingPath: [], debugDescription: "bad"))), .persistent)
        XCTAssertEqual(PushSetupFailure.classify(NSError(domain: NSOSStatusErrorDomain, code: -25300)), .persistent)
    }

    func testRetriesWaitFiveTwentyAndSixtySecondsThenStop() {
        XCTAssertEqual(PushSetupFailure.delay(afterFailure: 1, retryAfter: nil), 5)
        XCTAssertEqual(PushSetupFailure.delay(afterFailure: 2, retryAfter: nil), 20)
        XCTAssertEqual(PushSetupFailure.delay(afterFailure: 3, retryAfter: nil), 60)
        XCTAssertNil(PushSetupFailure.delay(afterFailure: 4, retryAfter: nil))
        XCTAssertNil(PushSetupFailure.delay(afterFailure: 0, retryAfter: nil))
    }

    func testRetryAfterIsHonouredUpToSixtySeconds() {
        XCTAssertEqual(PushSetupFailure.delay(afterFailure: 1, retryAfter: 30), 30)
        XCTAssertEqual(PushSetupFailure.delay(afterFailure: 2, retryAfter: 2), 2)
        XCTAssertEqual(PushSetupFailure.delay(afterFailure: 1, retryAfter: 600), 60)
        XCTAssertNil(PushSetupFailure.delay(afterFailure: 4, retryAfter: 1))
    }

    func testRetryAfterHeaderReadsSecondsAndHTTPDates() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        XCTAssertEqual(PushRelayBusy(status: 429, retryAfterHeader: "17", now: now).retryAfter, 17)
        XCTAssertEqual(PushRelayBusy(status: 503, retryAfterHeader: " 0 ", now: now).retryAfter, 0)
        let later = now.addingTimeInterval(45)
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(identifier: "GMT")
        formatter.dateFormat = "EEE, dd MMM yyyy HH:mm:ss zzz"
        XCTAssertEqual(PushRelayBusy(status: 503, retryAfterHeader: formatter.string(from: later), now: now).retryAfter, 45)
        XCTAssertNil(PushRelayBusy(status: 503, retryAfterHeader: nil, now: now).retryAfter)
        XCTAssertNil(PushRelayBusy(status: 503, retryAfterHeader: "soon", now: now).retryAfter)
        XCTAssertEqual(PushRelayBusy(status: 503, retryAfterHeader: "-5", now: now).retryAfter, 0)
    }
}
