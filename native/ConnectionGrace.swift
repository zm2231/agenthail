import Foundation

struct ConnectionGrace {
    var window: TimeInterval = 30
    private(set) var lastContact: Date?
    private var failingSince: Date?

    mutating func contact(at now: Date = Date()) {
        lastContact = now
        failingSince = nil
    }

    mutating func forget() {
        lastContact = nil
        failingSince = nil
    }

    // Only a dropped or slow transport is held; a refusal, an unpaired device or an incompatible daemon is shown at once.
    mutating func holds(_ error: Error, at now: Date = Date()) -> Bool {
        guard Self.isTransport(error), lastContact != nil else { return false }
        let since = failingSince ?? now
        failingSince = since
        return now.timeIntervalSince(since) < window
    }

    static func isTransport(_ error: Error) -> Bool {
        if let api = error as? AgenthailAPIError {
            if case .streamClosed = api { return true }
            return false
        }
        guard let url = error as? URLError else { return false }
        return transportCodes.contains(url.code)
    }

    private static let transportCodes: Set<URLError.Code> = [
        .timedOut, .networkConnectionLost, .notConnectedToInternet, .cannotConnectToHost, .cannotFindHost,
        .dnsLookupFailed, .dataNotAllowed, .internationalRoamingOff, .callIsActive, .cannotLoadFromNetwork,
    ]
}
