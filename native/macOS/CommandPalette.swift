import SwiftUI

struct CommandPalette: View {
    @ObservedObject var model: AgenthailModel
    @State private var query = ""
    @State private var highlighted = 0
    @FocusState private var fieldFocused: Bool

    private struct Entry: Identifiable {
        let id: String
        let icon: String
        let title: String
        let detail: String
        var session: SessionState? = nil
        var shortcut: String? = nil
        let run: () -> Void
    }

    private var entries: [Entry] {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        let attention = model.attentionSessionIDs
        let sessions = model.knownSessions
            .filter { session in
                trimmed.isEmpty || [session.title, session.name, session.alias, session.hostProject?.displayName, session.checkout?.branch]
                    .compactMap { $0 }
                    .contains { $0.localizedCaseInsensitiveContains(trimmed) }
            }
            .sorted { lhs, rhs in
                let left = (titleMatch(lhs, trimmed), attention.contains(lhs.id), lhs.isWorking)
                let right = (titleMatch(rhs, trimmed), attention.contains(rhs.id), rhs.isWorking)
                if left.0 != right.0 { return left.0 }
                if left.1 != right.1 { return left.1 }
                if left.2 != right.2 { return left.2 }
                return SessionTree.activity(lhs) > SessionTree.activity(rhs)
            }
            .prefix(trimmed.isEmpty ? 6 : 20)
            .map { session in
                Entry(
                    id: "session:\(session.id)",
                    icon: "",
                    title: session.title,
                    detail: [session.hostProject?.displayName, session.checkout?.branch, session.surface.capitalized].compactMap { $0 }.joined(separator: " · "),
                    session: session
                ) { model.selectSession(session.id) }
            }
        let actions = actionEntries.filter { trimmed.isEmpty || $0.title.localizedCaseInsensitiveContains(trimmed) }
        return sessions + actions
    }

    private func titleMatch(_ session: SessionState, _ query: String) -> Bool {
        !query.isEmpty && (session.title.localizedCaseInsensitiveContains(query) || session.name.localizedCaseInsensitiveContains(query))
    }

    private var actionEntries: [Entry] {
        var actions = [
            Entry(id: "new", icon: "square.and.pencil", title: "New session", detail: "", shortcut: "⌘N") { model.newSessionVisible = true },
            Entry(id: "inspector", icon: "sidebar.right", title: model.inspectorVisible ? "Hide inspector" : "Show inspector", detail: "", shortcut: "⌥⌘I") { model.inspectorVisible.toggle() },
        ]
        if let session = model.selectedSession {
            if session.isWorking {
                actions.append(Entry(id: "stop", icon: "stop.circle", title: "Stop \(session.title)", detail: "") { model.interruptSelected() })
            }
            if session.runtime?.focusable == true, let host = session.runtime?.hostName {
                actions.append(Entry(id: "focus", icon: "terminal", title: "Open in \(host)", detail: session.title) { model.focusInTerminal(session) })
            }
        }
        actions.append(Entry(id: "restart", icon: "arrow.clockwise", title: "Restart Agenthail", detail: "") { model.restartDaemon() })
        return actions
    }

    var body: some View {
        let items = entries
        VStack(spacing: 0) {
            HStack(spacing: 9) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(DesktopPalette.muted)
                TextField("Jump to a session or run a command", text: $query)
                    .textFieldStyle(.plain)
                    .font(.system(size: 15))
                    .focused($fieldFocused)
                    .onSubmit { run(items) }
                    .onKeyPress(.downArrow) { move(1, count: items.count); return .handled }
                    .onKeyPress(.upArrow) { move(-1, count: items.count); return .handled }
                    .onKeyPress(.escape) { model.paletteVisible = false; return .handled }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 12)
            Divider().overlay(DesktopPalette.line)
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 1) {
                        if items.isEmpty {
                            Text("No matches")
                                .font(.system(size: 13))
                                .foregroundStyle(DesktopPalette.muted)
                                .padding(14)
                        }
                        ForEach(Array(items.enumerated()), id: \.element.id) { index, entry in
                            row(entry, highlighted: index == highlighted)
                                .id(entry.id)
                                .onTapGesture { highlighted = index; run(items) }
                                .onHover { if $0 { highlighted = index } }
                        }
                    }
                    .padding(6)
                }
                .onChange(of: highlighted) { _, index in
                    if items.indices.contains(index) { proxy.scrollTo(items[index].id) }
                }
            }
            .frame(maxHeight: 360)
        }
        .frame(width: 560)
        .background(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(DesktopPalette.line2))
        .shadow(color: .black.opacity(0.35), radius: 24, y: 10)
        .task { fieldFocused = true }
        .onChange(of: query) { highlighted = 0 }
    }

    private func row(_ entry: Entry, highlighted: Bool) -> some View {
        HStack(spacing: 10) {
            Group {
                if let session = entry.session {
                    StatusIndicator(session: session, needsYou: model.attentionSessionIDs.contains(session.id), finishedUnseen: model.finishedUnseen.contains(session.id))
                } else {
                    Image(systemName: entry.icon)
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
            .frame(width: 18)
            Text(entry.title)
                .font(.system(size: 13))
                .foregroundStyle(DesktopPalette.text)
                .lineLimit(1)
            if !entry.detail.isEmpty {
                Text(entry.detail)
                    .font(.system(size: 12))
                    .foregroundStyle(DesktopPalette.muted)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            if let shortcut = entry.shortcut {
                Text(shortcut)
                    .font(.system(size: 11))
                    .foregroundStyle(DesktopPalette.muted)
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 7)
        .background(highlighted ? DesktopPalette.selection : Color.clear, in: RoundedRectangle(cornerRadius: 7))
        .contentShape(Rectangle())
    }

    private func move(_ offset: Int, count: Int) {
        guard count > 0 else { return }
        highlighted = min(max(highlighted + offset, 0), count - 1)
    }

    private func run(_ items: [Entry]) {
        guard items.indices.contains(highlighted) else { return }
        let entry = items[highlighted]
        model.paletteVisible = false
        entry.run()
    }
}
