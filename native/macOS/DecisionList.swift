import SwiftUI

struct DecisionList: View {
    var model: AgenthailModel
    var pane: SessionPane

    var body: some View {
        let feed = model.decisionFeed
        let inbox = feed.inbox
        let stops = model.inboxStops
        let selected = stops.selected(pane.selectedDecisionID)?.paneID
        if !stops.stopped.isEmpty {
            VStack(alignment: .leading, spacing: 2) {
                SidebarCaption("\(DecisionGroup.stopped.title) · \(stops.stopped.count)")
                ForEach(stops.stopped) { stop in
                    if case .decision(let item) = stop {
                        row(item, selected: item.id == selected)
                    } else {
                        stopRow(stop, selected: stop.paneID == selected)
                    }
                }
            }
        }
        ForEach([DecisionGroup.holding, .waiting], id: \.self) { group in
            let items = stops.decisions.filter { $0.group == group }
            if !items.isEmpty {
                VStack(alignment: .leading, spacing: 2) {
                    SidebarCaption("\(group.title) · \(items.count)")
                    ForEach(items) { item in
                        row(item, selected: item.id == selected)
                    }
                }
            }
        }
        if !inbox.leadHandling.isEmpty {
            VStack(alignment: .leading, spacing: 2) {
                SidebarCaption("Handled by a lead")
                ForEach(inbox.leadHandling) { item in
                    Button { pane.select(item.sessionId) } label: {
                        line(model.sessionName(item.sessionId), detail: "\(model.sessionName(item.heldBy)) has it · \(item.reason)")
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("decision-lead-" + item.sessionId)
                }
            }
        }
        ForEach(DecisionInboxPane.notes(inbox), id: \.self) { note in
            Text(note)
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
                .padding(.horizontal, DesktopSpace.s)
        }
        if inbox.isEmpty && stops.stopped.isEmpty {
            if feed.loaded {
                Text("Nothing needs you.")
                    .font(.system(size: DesktopType.body))
                    .foregroundStyle(DesktopPalette.text2)
                    .padding(.horizontal, DesktopSpace.s)
            } else {
                ProgressView().controlSize(.small).frame(maxWidth: .infinity).padding(.vertical, DesktopSpace.s)
            }
        }
    }

    private func row(_ item: DecisionItem, selected: Bool) -> some View {
        Button { pane.selectedDecisionID = item.id } label: {
            VStack(alignment: .leading, spacing: 2) {
                line(model.sessionName(item.sessionId), detail: DecisionInboxPane.summary(item))
                if let holds = item.holdsUp, !holds.isEmpty {
                    Text("Holds up \(holds.count)")
                        .font(.system(size: DesktopType.meta, weight: .medium))
                        .foregroundStyle(DesktopPalette.attention)
                        .padding(.horizontal, DesktopSpace.s)
                }
            }
        }
        .buttonStyle(.plain)
        .selectionMark(selected)
        .accessibilityIdentifier("decision-" + item.sessionId)
    }

    private func stopRow(_ stop: InboxStop, selected: Bool) -> some View {
        Button { pane.selectedDecisionID = stop.paneID } label: {
            line(stop.title(name: model.sessionName), detail: stop.summary)
        }
        .buttonStyle(.plain)
        .selectionMark(selected)
        .accessibilityIdentifier("inbox-stop-" + stop.id)
    }

    private func line(_ title: String, detail: String) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            Text(title)
                .font(.system(size: DesktopType.body))
                .foregroundStyle(DesktopPalette.text)
                .lineLimit(1)
            Text(detail)
                .font(.system(size: DesktopType.meta))
                .foregroundStyle(DesktopPalette.text2)
                .lineLimit(2)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, DesktopSpace.s)
        .padding(.vertical, DesktopSpace.xs)
        .contentShape(Rectangle())
    }
}
