import SwiftUI

struct DecisionInboxPane: View {
    var model: AgenthailModel
    var pane: SessionPane
    @State private var answering = false
    @State private var failure: String?
    @FocusState private var focused: Bool

    static func summary(_ item: DecisionItem) -> String {
        let requests = item.openRequests
        guard requests.count > 1 else { return item.reason }
        return requests.allSatisfy(\.usesPager) ? "\(requests.count) questions" : "\(requests.count) requests"
    }

    static func pagesQuestions(_ item: DecisionItem) -> Bool {
        item.answerable && item.openRequests.first?.usesPager == true
    }

    static func notes(_ inbox: DecisionInbox) -> [String] {
        inbox.unreachable.map { "\($0) isn't answering, so its decisions aren't shown." }
            + inbox.outdated.map { "\($0) runs an older Agenthail without the Inbox." }
    }

    var body: some View {
        let stops = model.inboxStops
        let entry = stops.selected(pane.selectedDecisionID)
        let following = entry.flatMap(stops.following)
        let item: DecisionItem? = if case .decision(let decision) = entry { decision } else { nil }
        Group {
            if let item {
                card(item, following: following)
            } else if let entry {
                InboxStopCard(model: model, pane: pane, stop: entry, following: following)
            } else {
                ContentUnavailableView(model.decisionFeed.loaded ? "Nothing needs you" : "Looking for decisions", systemImage: "tray")
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .focusable()
        .focused($focused)
        .focusEffectDisabled()
        .onKeyPress(characters: CharacterSet(charactersIn: "jk")) { press in
            move(press.characters == "j" ? 1 : -1, in: stops.entries, from: entry)
            return .handled
        }
        .onKeyPress(keys: [.return]) { press in
            guard DecisionInbox.acceptsRecommended(press.modifiers), !answering, let item, item.answerable,
                  let request = item.openRequests.first, let recommended = DecisionInbox.recommended(request) else { return .ignored }
            Task { await answer(request, recommended, item, following: following) }
            return .handled
        }
        .onChange(of: entry?.paneID, initial: true) {
            if !(item.map(Self.pagesQuestions) ?? false) { focused = true }
        }
        .scrimText()
        .backdropSurface(DesktopPalette.window)
    }

    private func card(_ item: DecisionItem, following: String?) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: DesktopSpace.m) {
                header(item)
                if let holds = item.holdsUp, !holds.isEmpty {
                    Text("Holds up: " + holds.map(model.sessionName).joined(separator: ", "))
                        .font(.system(size: DesktopType.secondary, weight: .medium))
                        .foregroundStyle(DesktopPalette.attention)
                }
                if item.answerable, let request = item.openRequests.first {
                    if item.openRequests.count > 1 {
                        Text("1 of \(item.openRequests.count)")
                            .font(.system(size: DesktopType.meta))
                            .foregroundStyle(DesktopPalette.text2)
                    }
                    RequestCard(request: request, answering: answering, accent: AnyShapeStyle(DesktopPalette.accentText),
                                respond: { decision in Task { await answer(request, decision, item, following: following) } },
                                asker: model.sessionName(item.sessionId), attention: AnyShapeStyle(DesktopPalette.attention),
                                surface: nil, actionTint: AnyShapeStyle(DesktopPalette.accent), focusesPager: true,
                                agentSurface: model.cells.session(item.sessionId)?.surface ?? "")
                        .id(item.sessionId + "/" + request.id)
                    if let recommended = DecisionInbox.recommended(request) {
                        Button("Accept recommended") { Task { await answer(request, recommended, item, following: following) } }
                            .help("Shift-Return")
                            .disabled(answering)
                            .accessibilityIdentifier("decision-accept-recommended")
                    }
                } else if let error = item.requestError {
                    Text(error).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
                }
                if let failure {
                    Text(failure).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.failed)
                }
                Button("Open session") { pane.select(item.sessionId) }
                    .accessibilityIdentifier("decision-open-session")
            }
            .frame(maxWidth: DesktopColumns.reading, alignment: .leading)
            .padding(DesktopSpace.xl)
        }
    }

    private func header(_ item: DecisionItem) -> some View {
        VStack(alignment: .leading, spacing: DesktopSpace.xs) {
            Text(item.group.title)
                .font(.system(size: DesktopType.caption, weight: .semibold))
                .foregroundStyle(DesktopPalette.text2)
            Text(model.sessionName(item.sessionId))
                .font(.system(size: DesktopType.display, weight: .semibold))
                .foregroundStyle(DesktopPalette.text)
            HStack(spacing: DesktopSpace.xs) {
                if let project = item.projectId.flatMap({ id in model.projects.first { $0.id == id } }) {
                    Text(project.displayName)
                }
                if let since = item.sinceDate {
                    Text(since, format: .relative(presentation: .named))
                }
            }
            .font(.system(size: DesktopType.secondary))
            .foregroundStyle(DesktopPalette.text2)
            Text(item.reason)
                .font(.system(size: DesktopType.body))
                .foregroundStyle(DesktopPalette.text)
        }
    }

    private func answer(_ request: SessionRequest, _ decision: RequestDecision, _ item: DecisionItem, following: String?) async {
        guard !answering else { return }
        answering = true
        defer { answering = false }
        failure = nil
        let answer = await pane.answerDecision(request, in: item.sessionId, decision: decision)
        if case .failed(let result) = answer { failure = result.message }
        if !model.decisionFeed.inbox.decisions.contains(where: { $0.id == item.id }) {
            pane.selectedDecisionID = following
        }
    }

    private func move(_ step: Int, in entries: [InboxStop], from entry: InboxStop?) {
        guard let entry, let index = entries.firstIndex(where: { $0.paneID == entry.paneID }), entries.indices.contains(index + step) else { return }
        pane.selectedDecisionID = entries[index + step].paneID
    }
}
