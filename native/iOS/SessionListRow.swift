import SwiftUI

struct SessionListRow: View {
    enum Source {
        case value(SessionState)
        case root(SessionFamily)
        case member(SessionFamily.Member)

        var session: SessionState {
            switch self {
            case let .value(session): session
            case let .root(family): family.root
            case let .member(member): member.session
            }
        }

        var family: SessionFamily? {
            if case let .root(family) = self { return family }
            return nil
        }

        var title: String? {
            if case let .member(member) = self { return SessionFamilies.label(member.session) }
            return nil
        }
    }

    var model: AgenthailIOSModel
    let source: Source
    var needsYou = false
    var selected = false
    var snippet: String? = nil
    var reason: String? = nil
    var identifierPrefix = "session-"
    let open: (String) -> Void

    var body: some View {
        let session = source.session
        let ring = SessionRing.kind(session, needsYou: needsYou)
        Button { open(session.id) } label: {
            HStack(alignment: .top, spacing: 12) {
                SessionAvatar(surface: session.surface, ring: ring)
                VStack(alignment: .leading, spacing: 5) {
                    IOSSessionRow(session: session, title: source.title, family: source.family, showsHost: model.showsHostNames)
                    if let reason {
                        Text(reason).font(.subheadline).foregroundStyle(PhonePalette.attention).lineLimit(2)
                    }
                    if let snippet, !snippet.isEmpty {
                        Text(snippet).font(.subheadline).foregroundStyle(PhonePalette.muted).lineLimit(2)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }.contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityValue(ring.spoken ?? "")
        .listRowBackground(GlassRowBackground(selected: selected))
        .swipeActions(edge: .trailing) {
            if snippet == nil {
                Button(session.archived == true ? "Unarchive" : "Archive", systemImage: "archivebox") {
                    Task { await model.setArchived(sessionID: session.id, session.archived != true) }
                }
            }
        }
        .accessibilityIdentifier(identifierPrefix + session.id)
    }
}
