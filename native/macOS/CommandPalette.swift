import SwiftUI

struct CommandPalette: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var pane: SessionPane
    @Environment(\.openWindow) private var openWindow
    @State private var query = ""
    @State private var highlightedID: String?
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
                ) { pane.select(session.id) }
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
            Entry(id: "inspector", icon: "sidebar.right", title: pane.inspectorVisible ? "Hide inspector" : "Show inspector", detail: "", shortcut: "⌥⌘I") { pane.inspectorVisible.toggle() },
        ]
        if let session = pane.selectedSession {
            if session.isWorking {
                actions.append(Entry(id: "stop", icon: "stop.circle", title: "Stop \(session.title)", detail: "") { pane.interrupt() })
            }
            actions.append(Entry(id: "window", icon: "macwindow.badge.plus", title: "Open \(session.title) in new window", detail: "") { openWindow(id: "session", value: session.id) })
            if session.runtime?.focusable == true, let host = session.runtime?.hostName {
                actions.append(Entry(id: "focus", icon: "terminal", title: "Open in \(host)", detail: session.title) { model.focusInTerminal(session) })
            }
        }
        actions.append(Entry(id: "restart", icon: "arrow.clockwise", title: "Restart Agenthail", detail: "") { model.restartDaemon() })
        return actions
    }

    var body: some View {
        let items = entries
        let highlighted = items.firstIndex { $0.id == highlightedID } ?? 0
        VStack(spacing: 0) {
            HStack(spacing: 9) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(DesktopPalette.muted)
                TextField("Jump to a session or run a command", text: $query)
                    .textFieldStyle(.plain)
                    .font(.system(size: 15))
                    .focused($fieldFocused)
                    .onSubmit { run(items, at: highlighted) }
                    .onKeyPress(.downArrow) { move(1, from: highlighted, in: items); return .handled }
                    .onKeyPress(.upArrow) { move(-1, from: highlighted, in: items); return .handled }
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
                                .onTapGesture { run(items, at: index) }
                                .onHover { if $0 { highlightedID = entry.id } }
                        }
                    }
                    .padding(6)
                }
                .onChange(of: highlightedID) { _, id in
                    if let id { proxy.scrollTo(id) }
                }
            }
            .frame(maxHeight: 360)
        }
        .frame(width: 560)
        .background(DesktopPalette.raised, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(DesktopPalette.line2))
        .shadow(color: .black.opacity(0.35), radius: 24, y: 10)
        .task { fieldFocused = true }
        .onChange(of: query) { highlightedID = nil }
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

    private func move(_ offset: Int, from index: Int, in items: [Entry]) {
        guard !items.isEmpty else { return }
        highlightedID = items[min(max(index + offset, 0), items.count - 1)].id
    }

    private func run(_ items: [Entry], at index: Int) {
        guard items.indices.contains(index) else { return }
        model.paletteVisible = false
        items[index].run()
    }
}
