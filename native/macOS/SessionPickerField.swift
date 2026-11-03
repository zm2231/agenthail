import SwiftUI

struct SessionPickerField: View {
    let title: String
    @Binding var selection: String
    @ObservedObject var model: AgenthailModel
    var writableOnly = false
    @State private var query = ""
    @State private var found: [SessionState] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            TextField("Find a session for \(title.lowercased())", text: $query)
            Picker(title, selection: $selection) {
                Text("Choose a session").tag("")
                ForEach(choices) { Text($0.displayName).tag($0.id) }
            }
        }
        .task(id: query) {
            let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmed.isEmpty else {
                found = []
                return
            }
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled else { return }
            found = await model.mentionSessions(trimmed)
        }
    }

    private var choices: [SessionState] {
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
        let local = model.knownSessions.filter { trimmed.isEmpty ? $0.current : $0.displayName.localizedCaseInsensitiveContains(trimmed) || $0.name.localizedCaseInsensitiveContains(trimmed) }
        var seen = Set<String>()
        let selected = model.knownSessions.filter { $0.id == selection } + found.filter { $0.id == selection }
        return (selected + local + found).filter { session in
            session.remoteHostID == nil && (!writableOnly || !session.isReadOnly) && seen.insert(session.id).inserted
        }
    }
}
