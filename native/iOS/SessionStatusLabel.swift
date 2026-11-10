import SwiftUI

struct SessionStatusLabel: View, @MainActor Equatable {
    let session: SessionState
    let waitingPlace: WaitingPlace?
    let limit: String?

    init(detail: SessionDetail, session: SessionState) {
        self.session = session
        waitingPlace = detail.waitingPlace
        limit = detail.limitLabel
    }

    var body: some View {
        TimelineView(.everyMinute) { context in
            if let move = session.move {
                Label(move.label, systemImage: "arrow.right.circle")
                    .font(.subheadline)
                    .foregroundStyle(PhonePalette.muted)
            } else {
                let kind = SessionDisplayKind.of(session, now: context.date)
                Label(SessionDisplayKind.label(session, now: context.date, waitingPlace: waitingPlace, limit: limit), systemImage: kind.symbol)
                    .font(.subheadline)
                    .foregroundStyle(kind == .working ? AnyShapeStyle(PhonePalette.working) : AnyShapeStyle(PhonePalette.muted))
            }
        }
    }
}
