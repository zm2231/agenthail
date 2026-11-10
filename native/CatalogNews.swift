import Foundation

/// Whether catalog events can change the daemon's session order. Rows update
/// in place either way; only news rereads the order.
enum CatalogNews {
    static func moves(_ events: [CatalogStreamEvent], held: [String: SessionState]) -> Bool {
        events.contains { event in
            switch event.type {
            case "session.upserted":
                guard let row = event.data.session, let placement = row.placement else { return true }
                return held[row.id]?.placement != placement
            case "session.removed":
                return event.data.sessionId.map { held[$0] != nil } ?? true
            case "session.queue":
                return true
            default:
                return event.type.hasPrefix("coordinator.")
            }
        }
    }
}
