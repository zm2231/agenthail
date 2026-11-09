import SwiftUI

struct SessionStatusLabel: View {
    let detail: SessionDetail
    let session: SessionState

    var body: some View {
        TimelineView(.everyMinute) { context in
            let kind = SessionDisplayKind.of(session, now: context.date)
            Label(SessionDisplayKind.label(session, now: context.date, waitingPlace: detail.waitingPlace, limit: detail.limitLabel), systemImage: kind.symbol)
                .font(.subheadline)
                .foregroundStyle(kind == .working ? AnyShapeStyle(PhonePalette.working) : AnyShapeStyle(PhonePalette.muted))
        }
    }
}
