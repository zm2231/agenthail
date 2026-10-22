import SwiftUI

struct SentPromptsList: View {
    let prompts: [PromptItem]
    let canFork: Bool
    let use: (PromptItem) -> Void
    let fork: (PromptItem) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Sent in this session")
                .font(.system(size: DesktopType.meta, weight: .semibold))
                .foregroundStyle(DesktopPalette.text2)
                .padding(.horizontal, 14)
                .padding(.top, DesktopSpace.s)
                .padding(.bottom, DesktopSpace.xs)
            if prompts.isEmpty {
                Text("Nothing sent yet.")
                    .font(.system(size: DesktopType.body))
                    .foregroundStyle(DesktopPalette.text2)
                    .padding(.horizontal, 14)
                    .padding(.bottom, DesktopSpace.s)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(prompts) { prompt in
                            HStack(alignment: .firstTextBaseline, spacing: DesktopSpace.s) {
                                Button { use(prompt) } label: {
                                    Text(prompt.text)
                                        .lineLimit(2)
                                        .foregroundStyle(DesktopPalette.text)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                        .contentShape(Rectangle())
                                }
                                .buttonStyle(.plain)
                                .help("Put this prompt in the composer")
                                if canFork, prompt.turnId != nil {
                                    Button("Fork from here") { fork(prompt) }
                                        .buttonStyle(.plain)
                                        .foregroundStyle(DesktopPalette.accentText)
                                        .help("Start a new conversation with everything before this prompt, and this prompt in its composer")
                                }
                            }
                            .font(.system(size: DesktopType.body))
                            .padding(.horizontal, 14)
                            .padding(.vertical, 6)
                        }
                    }
                }
                .frame(maxHeight: 320)
            }
        }
        .frame(width: 440)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Sent in this session")
    }
}

struct PromptSearchPanel: View {
    enum Scope: String, CaseIterable, Identifiable {
        case session = "This session"
        case all = "All sessions"
        var id: String { rawValue }
    }

    @ObservedObject var model: AgenthailModel
    let sessionID: String
    let use: (String) -> Void
    @State private var query = ""
    @State private var scope = Scope.session
    @State private var results: [PromptItem] = []
    @State private var problems: [String] = []
    @State private var searching = false
    @FocusState private var fieldFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.s) {
            HStack(spacing: DesktopSpace.s) {
                TextField("Search what you sent", text: $query)
                    .textFieldStyle(.roundedBorder)
                    .focused($fieldFocused)
                    .onSubmit { if let first = results.first { use(first.text) } }
                Picker("Scope", selection: $scope) {
                    ForEach(Scope.allCases) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .fixedSize()
            }
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    ForEach(results) { prompt in
                        Button { use(prompt.text) } label: {
                            VStack(alignment: .leading, spacing: 2) {
                                Text(prompt.text)
                                    .lineLimit(2)
                                    .foregroundStyle(DesktopPalette.text)
                                if scope == .all {
                                    Text(model.sessionTitle(for: prompt.sessionId))
                                        .font(.system(size: DesktopType.caption))
                                        .foregroundStyle(DesktopPalette.text2)
                                }
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .font(.system(size: DesktopType.body))
                        .padding(.vertical, 5)
                    }
                }
            }
            .frame(height: 280)
            .overlay {
                if results.isEmpty && !searching {
                    Text(query.isEmpty ? "Nothing sent yet." : "No sent prompt matches.")
                        .font(.system(size: DesktopType.body))
                        .foregroundStyle(DesktopPalette.text2)
                }
            }
            ForEach(problems, id: \.self) { problem in
                Text(problem)
                    .font(.system(size: DesktopType.meta))
                    .foregroundStyle(DesktopPalette.failed)
            }
        }
        .padding(DesktopSpace.m)
        .frame(width: 480)
        .onAppear { fieldFocused = true }
        .task(id: "\(scope.rawValue)\u{0}\(query)") {
            searching = true
            try? await Task.sleep(for: .milliseconds(query.isEmpty ? 0 : 200))
            guard !Task.isCancelled else { return }
            let found = await model.searchPrompts(query.trimmingCharacters(in: .whitespacesAndNewlines), sessionID: scope == .session ? sessionID : nil)
            guard !Task.isCancelled else { return }
            var seen = Set<String>()
            results = found.prompts.filter { seen.insert($0.text).inserted }
            problems = found.problems
            searching = false
        }
    }
}
