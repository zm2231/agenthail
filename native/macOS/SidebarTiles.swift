import SwiftUI

struct SidebarTiles: View {
    static let projectLimit = 5

    var model: AgenthailModel
    @ObservedObject var pane: SessionPane

    var body: some View {
        let inbox = model.decisionFeed.inbox
        let counts = inbox.needsYou(in: model.projects)
        let tiles = ProjectTiles.order(model.projects, limit: Self.projectLimit)
        let owner = model.snapshot?.daemon.host?.id ?? ""
        HStack(alignment: .top, spacing: 2) {
            tile("Inbox", mark: .symbol("tray"), count: inbox.decisions.count, selected: pane.scope == .inbox, id: "sidebar-tile-inbox", action: pane.showInbox)
            ForEach(tiles.shown) { project in
                tile(project.displayName, mark: .letter(ProjectTiles.letter(project.displayName), project: ProjectTiles.colourID(project.id, owner: owner)), count: counts[project.id] ?? 0,
                     selected: pane.scope == .project(project.id), id: "sidebar-tile-project-" + project.id) { pane.showScope(project: project.id) }
            }
            if !tiles.more.isEmpty {
                Menu {
                    ForEach(tiles.more) { project in
                        Button(project.archived == true ? "\(project.displayName) (archived)" : project.displayName) { pane.showScope(project: project.id) }
                    }
                } label: {
                    face("+\(tiles.more.count)", mark: .symbol("ellipsis"), count: tiles.more.reduce(0) { $0 + (counts[$1.id] ?? 0) },
                         selected: tiles.more.contains { pane.scope == .project($0.id) })
                }
                .menuStyle(.button)
                .menuIndicator(.hidden)
                .buttonStyle(.plain)
                .accessibilityIdentifier("sidebar-tile-more")
                .accessibilityLabel(moreTitle(tiles.more.count))
                .help(moreTitle(tiles.more.count))
            }
            tile("All", mark: .symbol("square.stack"), count: 0, selected: pane.scope == .all, id: "sidebar-tile-all", action: pane.showAll)
        }
        .padding(.horizontal, DesktopSpace.s)
        .padding(.bottom, DesktopSpace.xs)
    }

    private func moreTitle(_ count: Int) -> String {
        count == 1 ? "1 more project" : "\(count) more projects"
    }

    enum Mark {
        case symbol(String)
        case letter(String, project: String)

        var colours: (fill: Color, ink: AnyShapeStyle) {
            switch self {
            case .symbol: return (DesktopPalette.fill, AnyShapeStyle(DesktopPalette.text))
            case .letter(_, let project):
                let colour = DesktopPalette.project(project)
                return (colour.fill, AnyShapeStyle(colour.ink))
            }
        }
    }

    private func tile(_ title: String, mark: Mark, count: Int, selected: Bool, id: String, action: @escaping () -> Void) -> some View {
        Button(action: action) { face(title, mark: mark, count: count, selected: selected) }
            .buttonStyle(.plain)
            .help(title)
            .accessibilityIdentifier(id)
            .accessibilityLabel(title)
            .accessibilityValue(count > 0 ? "\(count) waiting" : "")
            .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private func face(_ title: String, mark: Mark, count: Int, selected: Bool) -> some View {
        VStack(spacing: 3) {
            Group {
                switch mark {
                case .symbol(let name): Image(systemName: name).font(.system(size: DesktopType.body, weight: .medium))
                case .letter(let letter, _): Text(letter).font(.system(size: DesktopType.body, weight: .semibold))
                }
            }
            .foregroundStyle(mark.colours.ink)
            .frame(width: 28, height: 28)
            .background(mark.colours.fill, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
            .overlay(alignment: .topTrailing) {
                if count > 0 {
                    Text("\(count)")
                        .font(.system(size: DesktopType.caption, weight: .semibold))
                        .monospacedDigit()
                        .foregroundStyle(DesktopPalette.attention)
                        .padding(.horizontal, 3)
                        .background(DesktopPalette.side, in: Capsule())
                        .offset(x: 7, y: -5)
                }
            }
            Text(title)
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(selected ? DesktopPalette.text : DesktopPalette.text2)
                .lineLimit(1)
                .truncationMode(.tail)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 4)
        .contentShape(Rectangle())
        .selectionMark(selected)
    }
}
