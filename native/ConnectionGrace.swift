import Foundation

struct ConnectionGrace {
    var window: TimeInterval = 15
    private(set) var lastContact: Date?

    mutating func contact(at now: Date = Date()) { lastContact = now }

    mutating func forget() { lastContact = nil }

    // Only a dropped or slow transport is held; a refusal, an unpaired device or an incompatible daemon is shown at once.
    func holds(_ error: Error, at now: Date = Date()) -> Bool {
        guard Self.isTransport(error), let lastContact else { return false }
        return now.timeIntervalSince(lastContact) < window
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
