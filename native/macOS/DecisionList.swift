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
        let select = { pane.selectedDecisionID = item.id }
        return Button(action: select) {
            VStack(alignment: .leading, spacing: 2) {
                line(model.sessionName(item.sessionId), subtitle: model.sessionTitle(item.sessionId), detail: DecisionInboxPane.summary(item)) { pane.select(item.sessionId) }
                if let holds = item.holdsUp, !holds.isEmpty {
                    Text("Holds up \(holds.count)")
                        .font(.system(size: DesktopType.meta, weight: .medium))
                        .foregroundStyle(DesktopPalette.attention)
                        .padding(.horizontal, DesktopSpace.s)
                }
                if let tag = DecisionInbox.tag(item, sentTo: model.decisionFeed.replies[item.id]) {
                    Text(tag)
                        .font(.system(size: DesktopType.meta, weight: .medium))
                        .foregroundStyle(DesktopPalette.text2)
                        .lineLimit(1)
                        .padding(.horizontal, DesktopSpace.s)
                        .padding(.bottom, DesktopSpace.xs)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .selectionMark(selected)
        .contextMenu {
            Button("Open session") { pane.select(item.sessionId) }
            Button(DecisionInbox.notNowTitle(item)) { Task { _ = await model.dismissDecision(item) } }
                .disabled(model.decisionFeed.dismissing.contains(item.id))
        }
        .accessibilityIdentifier("decision-" + item.id)
    }

    private func stopRow(_ stop: InboxStop, selected: Bool) -> some View {
        Button { pane.selectedDecisionID = stop.paneID } label: {
            line(stop.title(name: model.sessionName), detail: stop.summary)
        }
        .buttonStyle(.plain)
        .selectionMark(selected)
        .accessibilityIdentifier("inbox-stop-" + stop.id)
    }

    private func line(_ title: String, subtitle: String? = nil, detail: String, open: (() -> Void)? = nil) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            if let open {
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: DesktopSpace.xs) {
                        sessionLink(title, open: open)
                        if let subtitle {
                            Text(subtitle)
                                .font(.system(size: DesktopType.meta))
                                .foregroundStyle(DesktopPalette.text2)
                                .lineLimit(1)
                                .fixedSize()
                        }
                    }
                    sessionLink(title, open: open)
                }
            } else {
                Text(title)
                    .font(.system(size: DesktopType.body))
                    .foregroundStyle(DesktopPalette.text)
                    .lineLimit(1)
            }
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

    private func sessionLink(_ title: String, open: @escaping () -> Void) -> some View {
        Button(action: open) {
            Text(title)
                .font(.system(size: DesktopType.body))
                .foregroundStyle(DesktopPalette.text)
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .buttonStyle(.plain)
        .layoutPriority(1)
        .onHover { inside in if inside { NSCursor.pointingHand.push() } else { NSCursor.pop() } }
        .help("Open \(title)")
        .accessibilityIdentifier("decision-session-link")
    }
}
