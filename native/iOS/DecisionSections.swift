import SwiftUI

struct DecisionSections: View {
    @ObservedObject var model: AgenthailIOSModel
    let inbox: DecisionInbox
    let open: (String) -> Void
    let changed: () -> Void
    @State private var answering: DecisionItem?

    var body: some View {
        Group {
            ForEach(inbox.unreachable, id: \.self) { name in
                Label("\(name) isn't answering. Its decisions aren't shown.", systemImage: "wifi.exclamationmark")
                    .foregroundStyle(PhonePalette.muted).font(.footnote)
                    .accessibilityIdentifier("decisions-unreachable-\(name)")
            }
            ForEach(inbox.outdated, id: \.self) { name in
                Label("Update Agenthail on \(name) to see its decisions here.", systemImage: "arrow.up.circle")
                    .foregroundStyle(PhonePalette.muted).font(.footnote)
                    .accessibilityIdentifier("decisions-outdated-\(name)")
            }
            ForEach(DecisionGroup.allCases, id: \.self) { group in
                let items = inbox.items(in: group)
                if !items.isEmpty {
                    Section("\(group.title) · \(items.count)") {
                        ForEach(items) { row($0) }
                    }
                }
            }
            if !inbox.leadHandling.isEmpty {
                Section("Handled by a lead") {
                    ForEach(inbox.leadHandling) { item in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(name(of: item.sessionId)).font(.subheadline)
                            Text("\(name(of: item.heldBy)) is answering its prompt").font(.footnote).foregroundStyle(PhonePalette.muted)
                        }
                        .accessibilityElement(children: .combine)
                        .accessibilityIdentifier("decision-lead-\(item.sessionId)")
                    }
                }
            }
        }
        .sheet(item: $answering) { item in
            DecisionSheet(model: model, item: item, title: name(of: item.sessionId), open: { answering = nil; open(item.sessionId) }) {
                answering = nil
                changed()
            }
        }
    }

    private func row(_ item: DecisionItem) -> some View {
        Button {
            if item.answerable { answering = item } else { open(item.sessionId) }
        } label: {
            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline) {
                    Text(place(of: item)).font(.footnote).foregroundStyle(PhonePalette.muted).lineLimit(1)
                    Spacer()
                    if let since = item.sinceDate {
                        Text(since, format: .relative(presentation: .numeric, unitsStyle: .abbreviated)).font(.footnote).foregroundStyle(PhonePalette.muted)
                    }
                }
                Text(headline(of: item)).font(.headline).lineLimit(2)
                HStack(spacing: 8) {
                    if let holds = item.holdsUp, !holds.isEmpty {
                        Text("Holds up \(holds.count)").font(.caption.weight(.semibold)).foregroundStyle(PhonePalette.attention)
                    }
                    if item.openRequests.count > 1 {
                        Text("\(item.openRequests.count) requests").font(.caption).foregroundStyle(PhonePalette.muted)
                    }
                    Text(item.reason)
                        .font(.caption).foregroundStyle(PhonePalette.muted).lineLimit(2)
                }
            }
            .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityHint(item.answerable ? "Opens the request to answer it" : "Opens the session")
        .accessibilityIdentifier("decision-\(item.sessionId)")
    }

    private func headline(of item: DecisionItem) -> String {
        if let request = item.openRequests.first {
            return request.isAsk ? (request.title ?? request.summary) : request.summary
        }
        return name(of: item.sessionId)
    }

    private func place(of item: DecisionItem) -> String {
        let project = item.projectId.flatMap { model.projects[$0]?.state.displayName }
        return [project, name(of: item.sessionId)].compactMap { $0 }.joined(separator: " · ")
    }

    private func name(of sessionID: String) -> String {
        model.snapshot?.sessions.first { $0.id == sessionID }?.displayName ?? sessionID
    }
}

struct DecisionSheet: View {
    @ObservedObject var model: AgenthailIOSModel
    let item: DecisionItem
    let title: String
    let open: () -> Void
    let done: () -> Void
    @State private var remaining: [SessionRequest] = []
    @State private var failure: String?

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    Text(item.reason).font(.footnote).foregroundStyle(PhonePalette.muted)
                    if let holds = item.holdsUp, !holds.isEmpty {
                        Text("Holds up \(holds.map(name).formatted(.list(type: .and)))").font(.footnote).foregroundStyle(PhonePalette.attention)
                            .accessibilityIdentifier("decision-holds-up")
                    }
                    if let failure {
                        Label(failure, systemImage: "exclamationmark.triangle").font(.footnote).foregroundStyle(PhonePalette.failed)
                            .accessibilityIdentifier("decision-failure")
                    }
                    if let request = remaining.first {
                        if remaining.count > 1 {
                            Text("Request 1 of \(remaining.count)").font(.caption).foregroundStyle(PhonePalette.muted)
                        }
                        RequestCard(request: request, answering: model.answeringRequest != nil, accent: AnyShapeStyle(PhonePalette.accentText),
                                    respond: { decision in Task { await answer(request, decision) } },
                                    asker: title, attention: AnyShapeStyle(PhonePalette.attention), actionTint: AnyShapeStyle(PhonePalette.accent),
                                    agentSurface: model.snapshot?.sessions.first { $0.id == item.sessionId }?.surface ?? "")
                            .id(request.id)
                    }
                }
                .padding()
            }
            .phoneBackdrop(.pane)
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Close", action: done) }
                ToolbarItem(placement: .primaryAction) { Button("Open session", action: open).accessibilityIdentifier("decision-open-session") }
            }
        }
        .onAppear { remaining = item.openRequests }
    }

    private func answer(_ request: SessionRequest, _ decision: RequestDecision) async {
        switch await model.answerDecision(request, sessionID: item.sessionId, decision: decision) {
        case .answered:
            failure = nil
            next(after: request)
        case .failed(let result):
            failure = result.message
            if result.dropsCard { next(after: request) }
        case .busy:
            break
        }
    }

    private func next(after request: SessionRequest) {
        remaining.removeAll { $0.id == request.id }
        if remaining.isEmpty { done() }
    }

    private func name(_ sessionID: String) -> String {
        model.snapshot?.sessions.first { $0.id == sessionID }?.displayName ?? sessionID
    }
}
