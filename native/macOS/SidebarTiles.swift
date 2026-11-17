import SwiftUI

struct SidebarTiles: View {
    @State private var width = DesktopColumns.sidebar.ideal - 2 * DesktopSpace.s

    var model: AgenthailModel
    var pane: SessionPane
    var tileOrder = TileOrderStore.mac

    var body: some View {
        let stops = model.inboxStops
        let counts = model.needsYou(stops)
        let leads = model.projects.reduce(into: [String: ProjectTiles.Lead]()) { leads, project in
            if let session = project.leads.first.flatMap(ProjectContents.leadSessionID).flatMap(model.session) { leads[project.id] = ProjectTiles.Lead(session) }
        }
        let count = TileGrid.columns(width: width)
        let pinned = ExtensionEntries.pinned(model.extensionCatalogs.flatMap(\.entries), pins: ExtensionPins.shared.ids)
        let tiles = TileGrid.fill(columns: count, fixed: 3 + pinned.count) { limit in
            tileOrder.arrangement.order.isEmpty ? ProjectTiles.order(model.projects, limit: limit, waiting: counts, leads: leads) : tileOrder.tiles(model.projects, limit: limit)
        }
        let arranged = (tiles.shown + tiles.more).map(\.id)
        let owner = model.daemon?.host?.id ?? ""
        let columns = Array(repeating: GridItem(.flexible(), spacing: TileGrid.spacing, alignment: .top), count: count)
        LazyVGrid(columns: columns, alignment: .leading, spacing: TileGrid.spacing) {
            tile("Inbox", mark: .symbol("tray"), count: model.needsYouCount, selected: pane.scope == .inbox, id: "sidebar-tile-inbox", action: pane.showInbox)
            tile("Apps", mark: .symbol(ExtensionEntry.fallbackIcon), count: 0, selected: pane.scope == .extensions, id: "sidebar-tile-extensions", action: pane.showExtensions)
            ForEach(pinned) { entry in
                tile(entry.label, mark: .symbol(entry.icon), count: 0, badge: entry.badge, selected: pane.scope == .extensionEntry(entry.id), id: "sidebar-tile-extension-" + entry.id) { pane.showExtension(entry.id) }
            }
            ForEach(tiles.shown) { project in
                tile(project.displayName, mark: .letter(ProjectTiles.letter(project.displayName), project: ProjectTiles.colourID(project.id, owner: owner)), count: counts[project.id] ?? 0,
                     pinned: tileOrder.isPinned(project.id), selected: pane.scope == .project(project.id), id: "sidebar-tile-project-" + project.id) { pane.showScope(project: project.id) }
                    .draggable(project.id)
                    .dropDestination(for: String.self) { dropped, _ in
                        guard let id = dropped.first, arranged.contains(id) else { return false }
                        tileOrder.change(.move(id, before: project.id), arranged: arranged)
                        return true
                    }
                    .contextMenu {
                        Button(tileOrder.isPinned(project.id) ? "Unpin" : "Pin to Front") { tileOrder.change(.pin(project.id, !tileOrder.isPinned(project.id)), arranged: arranged) }
                        Button("Move Earlier") { tileOrder.change(.step(project.id, by: -1), arranged: arranged) }
                        Button("Move Later") { tileOrder.change(.step(project.id, by: 1), arranged: arranged) }
                    }
            }
            if !tiles.more.isEmpty {
                Menu {
                    ForEach(tiles.more) { project in
                        Button { pane.showScope(project: project.id) } label: {
                            Text(project.displayName)
                            Text(moreDetail(project, waiting: counts[project.id] ?? 0))
                        }
                        .accessibilityLabel(project.displayName)
                        .accessibilityValue(moreDetail(project, waiting: counts[project.id] ?? 0))
                    }
                } label: {
                    face("+\(tiles.more.count)", mark: .symbol("ellipsis"), count: tiles.more.reduce(0) { $0 + (counts[$1.id] ?? 0) },
                         selected: tiles.more.contains { pane.scope == .project($0.id) })
                        .accessibilityHidden(true)
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
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
        .onChange(of: TileSync(remote: model.tileOrder, api: model.api.map(ObjectIdentifier.init)), initial: true) {
            let api = model.api
            tileOrder.adopt(model.tileOrder) { state in
                guard let api else { throw AgenthailAPIError.unavailable(AgenthailAPIError.disconnectedGuidance) }
                return try await api.putTileOrder(state)
            }
        }
        .padding(.horizontal, DesktopSpace.s)
        .padding(.bottom, DesktopSpace.xs)
    }

    private struct TileSync: Equatable {
        var remote: TileOrderState?
        var api: ObjectIdentifier?
    }

    private func moreDetail(_ project: ProjectState, waiting: Int) -> String {
        var parts: [String] = []
        if project.archived == true { parts.append("Archived") }
        if waiting > 0 { parts.append("\(waiting) need\(waiting == 1 ? "s" : "") you") }
        let sessions = project.activity?.sessionCount ?? 0
        if sessions > 0 { parts.append(sessions == 1 ? "1 session" : "\(sessions) sessions") }
        return parts.isEmpty ? "No sessions" : parts.joined(separator: " · ")
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

    private func tile(_ title: String, mark: Mark, count: Int, badge: String? = nil, pinned: Bool = false, selected: Bool, id: String, action: @escaping () -> Void) -> some View {
        Button(action: action) { face(title, mark: mark, count: count, badge: badge, pinned: pinned, selected: selected) }
            .buttonStyle(.plain)
            .help(title)
            .accessibilityIdentifier(id)
            .accessibilityLabel(title)
            .accessibilityValue([pinned ? "Pinned" : nil, count > 0 ? "\(count) waiting" : badge].compactMap { $0 }.joined(separator: ", "))
            .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private func face(_ title: String, mark: Mark, count: Int, badge: String? = nil, pinned: Bool = false, selected: Bool) -> some View {
        VStack(spacing: 3) {
            Group {
                switch mark {
                case .symbol(let name): ExtensionIcon(name: name).font(.system(size: DesktopType.body, weight: .medium))
                case .letter(let letter, _): Text(letter).font(.system(size: DesktopType.body, weight: .semibold))
                }
            }
            .foregroundStyle(mark.colours.ink)
            .frame(width: 28, height: 28)
            .background(mark.colours.fill, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
            .overlay(alignment: .topLeading) {
                if pinned {
                    Image(systemName: "pin.fill")
                        .font(.system(size: DesktopType.caption - 2, weight: .semibold))
                        .foregroundStyle(DesktopPalette.text2)
                        .padding(2)
                        .background(DesktopPalette.side, in: Circle())
                        .offset(x: -6, y: -5)
                }
            }
            .overlay(alignment: .topTrailing) {
                if let shown = count > 0 ? "\(count)" : badge {
                    Text(shown)
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
