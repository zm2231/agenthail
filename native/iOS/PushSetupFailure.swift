import Foundation

enum PushSetupFailure: Equatable {
    case transient(retryAfter: TimeInterval?)
    case persistent

    static let backoff: [TimeInterval] = [5, 20, 60]

    static func classify(_ error: Error) -> PushSetupFailure {
        if error is CancellationError { return .transient(retryAfter: nil) }
        if let busy = error as? PushRelayBusy { return .transient(retryAfter: busy.retryAfter) }
        if let url = error as? URLError { return transientCodes.contains(url.code) ? .transient(retryAfter: nil) : .persistent }
        if case let AgenthailAPIError.request(status, _) = error, status == 429 || (500..<600).contains(status) {
            return .transient(retryAfter: nil)
        }
        return .persistent
    }

    static func delay(afterFailure failures: Int, retryAfter: TimeInterval?) -> TimeInterval? {
        guard failures >= 1, failures <= backoff.count, let cap = backoff.last else { return nil }
        return min(retryAfter ?? backoff[failures - 1], cap)
    }

    private static let transientCodes: Set<URLError.Code> = [
        .networkConnectionLost, .notConnectedToInternet, .timedOut, .cannotConnectToHost,
        .cannotFindHost, .dnsLookupFailed, .internationalRoamingOff, .dataNotAllowed, .cancelled,
    ]
}

struct PushRelayBusy: LocalizedError, Equatable {
    let status: Int
    let retryAfter: TimeInterval?

    var errorDescription: String? { "The notification service is busy (HTTP \(status))." }

    init(status: Int, retryAfter: TimeInterval?) {
        self.status = status
        self.retryAfter = retryAfter
    }

    init(status: Int, retryAfterHeader: String?, now: Date = Date()) {
        self.status = status
        guard let header = retryAfterHeader?.trimmingCharacters(in: .whitespaces), !header.isEmpty else {
            retryAfter = nil
            return
        }
        if let seconds = TimeInterval(header) {
            retryAfter = max(0, seconds)
        } else if let date = Self.httpDate.date(from: header) {
            retryAfter = max(0, date.timeIntervalSince(now))
        } else {
            retryAfter = nil
        }
    }

    private static let httpDate: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(identifier: "GMT")
        formatter.dateFormat = "EEE, dd MMM yyyy HH:mm:ss zzz"
        return formatter
    }()
}
