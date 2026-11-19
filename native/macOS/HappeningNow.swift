import SwiftUI

struct HappeningNow: View {
    var model: AgenthailModel
    var pane: SessionPane
    let session: SessionState
    @State private var ready: Int?
    @State private var showsAllSteps = false

    var body: some View {
        let crew = LeadCrew.ordered(LeadCrew.crew(of: session.id, in: model.sessionTree))
        VStack(alignment: .leading, spacing: DesktopSpace.m) {
            HStack(alignment: .firstTextBaseline) {
                SidebarCaption("Happening now").padding(.horizontal, -8)
                Spacer(minLength: DesktopSpace.s)
                if let ready, ready > 0, let project = session.project {
                    Button("\(ready) ready") { pane.showProject(project.id) }
                        .buttonStyle(.plain)
                        .font(.system(size: DesktopType.secondary))
                        .foregroundStyle(DesktopPalette.accentText)
                        .help("Open this project's work list")
                        .accessibilityIdentifier("happening-ready")
                }
            }
            ForEach(crew) { child in
                CrewCard(model: model, pane: pane, child: child)
                    .equatable()
            }
            LeadSteps(items: pane.timelineItems, showsAll: $showsAllSteps)
        }
        .task(id: session.project?.key) {
            ready = nil
            guard let api = model.api, let key = session.project?.key, !key.isEmpty else { return }
            await LedgerReady.follow(api: api, projectKey: key) { ready = $0 }
        }
    }
}

private struct LeadSteps: View {
    let items: [TimelineItem]
    @Binding var showsAll: Bool

    var body: some View {
        let steps = LeadCrew.stepsThisTurn(items)
        if !steps.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                SidebarCaption("Lead's work this turn").padding(.horizontal, -8)
                let shown = showsAll ? steps : Array(steps.suffix(3))
                ForEach(Array(shown.enumerated()), id: \.offset) { _, step in
                    Text(step)
                        .lineLimit(1)
                        .truncationMode(.middle)
                        .foregroundStyle(DesktopPalette.text2)
                }
                if steps.count > shown.count {
                    Button(steps.count - shown.count == 1 ? "1 more step ›" : "\(steps.count - shown.count) more steps ›") { showsAll = true }
                        .buttonStyle(.plain)
                        .foregroundStyle(DesktopPalette.muted)
                } else if showsAll && steps.count > 3 {
                    Button("Fewer steps") { showsAll = false }
                        .buttonStyle(.plain)
                        .foregroundStyle(DesktopPalette.muted)
                }
            }
            .font(.system(size: DesktopType.secondary))
        }
    }
}

struct CrewCard: View, @MainActor Equatable {
    var model: AgenthailModel
    var pane: SessionPane
    let child: SessionState
    @State private var requests: SessionRequests?
    @State private var answering = false
    @State private var failure: String?

    static func == (lhs: Self, rhs: Self) -> Bool { lhs.child == rhs.child }

    var body: some View {
        let request = LeadCrew.asksYou(child) ? requests?.offered(pendingAsk: child.pendingAskID, open: child.pendingAsks, retired: pane.retiredAsks.retired(in: child.id)).first : nil
        VStack(alignment: .leading, spacing: 6) {
            Button { pane.select(child.id) } label: {
                HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
                    StatusIndicator(session: child, needsYou: LeadCrew.needsYou(child))
                    TimelineView(.everyMinute) { context in
                        let name = Text(child.displayName).font(.system(size: DesktopType.body, weight: .semibold))
                        let state = Text(LeadCrew.stateLabel(child, now: context.date))
                            .font(.system(size: DesktopType.secondary))
                            .foregroundStyle(LeadCrew.needsYou(child) ? DesktopPalette.attention : DesktopPalette.text2)
                        ViewThatFits(in: .horizontal) {
                            HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
                                name.lineLimit(1).fixedSize()
                                Spacer(minLength: 4)
                                state.lineLimit(1).fixedSize()
                            }
                            VStack(alignment: .leading, spacing: 2) {
                                name.lineLimit(1).truncationMode(.middle)
                                state.lineLimit(1).truncationMode(.middle)
                            }
                        }
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("crew-" + child.id)
            if let headline = request == nil ? LeadCrew.headline(child) : nil {
                Text(headline)
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.text2)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let plan = child.livePlan {
                ProgressView(value: Double(plan.done), total: Double(plan.total))
                    .controlSize(.small)
                    .accessibilityLabel(plan.spokenLabel)
            }
            if let request {
                RequestCard(request: request, answering: answering, accent: AnyShapeStyle(DesktopPalette.accentText),
                            respond: { decision in Task { await answer(request, decision) } },
                            asker: child.displayName, attention: AnyShapeStyle(DesktopPalette.attention),
                            surface: nil, actionTint: AnyShapeStyle(DesktopPalette.accent), agentSurface: child.surface)
                    .id(child.id + "/" + request.id)
            }
            if let failure {
                Text(failure)
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.failed)
            }
        }
        .padding(DesktopSpace.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(DesktopPalette.bubble, in: RoundedRectangle(cornerRadius: DesktopRadius.row))
        .task(id: RequestSync.viewKey(child)) {
            guard LeadCrew.asksYou(child), RequestSync.lists(child), let api = model.api else {
                requests = nil
                return
            }
            requests = try? await api.requests(sessionID: child.id)
        }
    }

    private func answer(_ request: SessionRequest, _ decision: RequestDecision) async {
        answering = true
        failure = nil
        switch await pane.answerDecision(request, in: child.id, decision: decision) {
        case .answered: requests = requests?.removing(request.id)
        case .failed(let error):
            if error.dropsCard { requests = requests?.removing(request.id) }
            failure = error.message
        case .busy: break
        }
        answering = false
    }
}
