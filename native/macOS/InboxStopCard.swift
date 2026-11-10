import SwiftUI

struct InboxStopCard: View {
    var model: AgenthailModel
    var pane: SessionPane
    let stop: InboxStop
    let following: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: DesktopSpace.m) {
                VStack(alignment: .leading, spacing: DesktopSpace.xs) {
                    Text(DecisionGroup.stopped.title)
                        .font(.system(size: DesktopType.caption, weight: .semibold))
                        .foregroundStyle(DesktopPalette.text2)
                    Text(stop.title(name: model.sessionName))
                        .font(.system(size: DesktopType.display, weight: .semibold))
                        .foregroundStyle(DesktopPalette.text)
                    if let when = stop.at.flatMap(TimestampParser.date) {
                        Text(when, format: .relative(presentation: .named))
                            .font(.system(size: DesktopType.secondary))
                            .foregroundStyle(DesktopPalette.text2)
                    }
                }
                ForEach(stop.lines, id: \.self) { line in
                    Text(line).font(.system(size: DesktopType.body)).foregroundStyle(DesktopPalette.text).textSelection(.enabled)
                }
                HStack(spacing: DesktopSpace.s) { actions }
            }
            .frame(maxWidth: DesktopColumns.reading, alignment: .leading)
            .padding(DesktopSpace.xl)
        }
    }

    @ViewBuilder
    private var actions: some View {
        switch stop {
        case .replacement(let item):
            Button("Open") { pane.select(item.sessionId) }.accessibilityIdentifier("inbox-stop-open")
            Button("Acknowledge") {
                model.acknowledgeLeadReplacement(item)
                pane.selectedDecisionID = following
            }.accessibilityIdentifier("inbox-stop-acknowledge")
        case .decision:
            EmptyView()
        }
    }
}
