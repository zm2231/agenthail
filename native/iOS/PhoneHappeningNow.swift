import SwiftUI

struct IOSNowButton: View {
    var model: AgenthailIOSModel
    let sessionID: String
    @Binding var showing: Bool

    var body: some View {
#if DEBUG
        let _ = BodyRuns.hit("IOSNowButton")
#endif
        let crew = LeadCrew.crew(of: sessionID, in: model.sessionTree(search: ""))
        if !crew.isEmpty {
            let waiting = LeadCrew.needsYou(crew)
            Button { showing = true } label: {
                Image(systemName: waiting > 0 ? "person.2.badge.gearshape.fill" : "person.2")
                    .overlay(alignment: .topTrailing) {
                        if waiting > 0 {
                            Text("\(waiting)")
                                .font(.caption2.weight(.bold))
                                .foregroundStyle(.white)
                                .padding(3)
                                .background(Circle().fill(PhonePalette.attention))
                                .offset(x: 8, y: -8)
                        }
                    }
            }
            .accessibilityLabel("Happening now")
            .accessibilityValue(waiting > 0 ? "\(waiting) need you" : "\(crew.count) working with this session")
            .accessibilityIdentifier("happening-now")
        }
    }
}

struct PhoneHappeningNow: View {
    var model: AgenthailIOSModel
    let session: SessionState
    let items: [TimelineItem]
    var open: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var ready: Int?
    @State private var showsAllSteps = false

    var body: some View {
#if DEBUG
        let _ = BodyRuns.hit("PhoneHappeningNow")
#endif
        let crew = LeadCrew.ordered(LeadCrew.crew(of: session.id, in: model.sessionTree(search: "")))
        let steps = LeadCrew.stepsThisTurn(items)
        NavigationStack {
            List {
                Section {
                    ForEach(crew) { child in
                        PhoneCrewCard(model: model, child: child) { open(child.id); dismiss() }
                            .equatable()
                    }
                } header: {
                    HStack {
                        Text("Happening now")
                        Spacer()
                        if let ready, ready > 0, let project = session.project {
                            Button("\(ready) ready") { open(ConversationRoute.project(project.id)); dismiss() }
                                .foregroundStyle(PhonePalette.accentText)
                                .textCase(nil)
                                .accessibilityHint("Opens this project's work list")
                                .accessibilityIdentifier("happening-ready")
                        }
                    }
                }
                if !steps.isEmpty {
                    Section("Lead's work this turn") {
                        let shown = showsAllSteps ? steps : Array(steps.suffix(3))
                        ForEach(Array(shown.enumerated()), id: \.offset) { _, step in
                            Text(step).font(.subheadline).foregroundStyle(PhonePalette.muted).lineLimit(1).truncationMode(.middle)
                        }
                        if steps.count > shown.count {
                            Button(steps.count - shown.count == 1 ? "1 more step" : "\(steps.count - shown.count) more steps") { showsAllSteps = true }
                        }
                    }
                }
            }
            .navigationTitle(model.cells.title(of: session))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
        }
        .task(id: session.project?.key) {
            ready = nil
            guard let key = session.project?.key, !key.isEmpty, let api = model.api(for: session.id) else { return }
            await LedgerReady.follow(api: api, projectKey: key) { ready = $0 }
        }
    }
}

struct PhoneCrewCard: View, @MainActor Equatable {
    var model: AgenthailIOSModel
    let child: SessionState
    var open: () -> Void
    @State private var requests: SessionRequests?

    static func == (lhs: Self, rhs: Self) -> Bool { lhs.child == rhs.child }

    var body: some View {
#if DEBUG
        let _ = BodyRuns.hit("PhoneCrewCard")
        let _ = BodyRuns.hit("PhoneCrewCard", id: child.id)
#endif
        let request = LeadCrew.asksYou(child) ? requests?.offered(pendingAsk: child.pendingAskID, open: child.pendingAsks).first : nil
        VStack(alignment: .leading, spacing: 6) {
            Button(action: open) {
                HStack(spacing: 8) {
                    Circle().fill(LeadCrew.needsYou(child) ? AnyShapeStyle(PhonePalette.attention) : child.isWorking ? AnyShapeStyle(PhonePalette.working) : AnyShapeStyle(PhonePalette.muted))
                        .frame(width: 8, height: 8)
                        .accessibilityHidden(true)
                    Text(child.displayName).font(.body.weight(.semibold)).lineLimit(1).truncationMode(.middle)
                    Spacer(minLength: 4)
                    Text(LeadCrew.stateLabel(child)).font(.subheadline)
                        .foregroundStyle(LeadCrew.needsYou(child) ? AnyShapeStyle(PhonePalette.attention) : AnyShapeStyle(PhonePalette.muted))
                        .lineLimit(1)
                        .fixedSize()
                }
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("crew-" + child.id)
            if request == nil, let headline = LeadCrew.headline(child) {
                Text(headline).font(.subheadline).foregroundStyle(PhonePalette.muted).lineLimit(3)
            }
            if let plan = child.livePlan {
                ProgressView(value: Double(plan.done), total: Double(plan.total))
                    .accessibilityLabel(plan.spokenLabel)
            }
            if let request {
                RequestCard(request: request, answering: model.answeringRequest != nil, accent: AnyShapeStyle(PhonePalette.accentText),
                            respond: { decision in Task {
                                await model.respond(to: request, sessionID: child.id, decision: decision)
                                requests = await model.openRequests(of: child.id)
                            } },
                            asker: child.displayName, attention: AnyShapeStyle(PhonePalette.attention), actionTint: AnyShapeStyle(PhonePalette.accent),
                            agentSurface: child.surface)
                    .id(child.id + "/" + request.id)
            }
        }
        .padding(.vertical, 4)
        .task(id: RequestSync.viewKey(child)) {
            guard LeadCrew.asksYou(child), RequestSync.lists(child) else {
                requests = nil
                return
            }
            requests = await model.openRequests(of: child.id)
        }
    }
}
