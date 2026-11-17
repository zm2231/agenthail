import SwiftUI

struct PhoneProjectTiles: View {
    var model: AgenthailIOSModel
    let open: (String) -> Void
    @Environment(\.horizontalSizeClass) private var sizeClass
    @Environment(\.dynamicTypeSize) private var typeSize
    @ScaledMetric(relativeTo: .body) private var mark: CGFloat = 44
    @State private var arranging: ProjectState?

    static func limit(_ sizeClass: UserInterfaceSizeClass?) -> Int { sizeClass == .regular ? 6 : 4 }

    static func moreTitle(_ count: Int) -> String { count == 1 ? "1 more project" : "\(count) more projects" }

    var body: some View {
        let projects = model.qualifiedProjects
        let counts = model.inboxStops.needsYou(in: projects, projectOf: model.projectOfSession())
        let ranked = ProjectTiles.order(projects, limit: Self.limit(sizeClass), waiting: counts, leads: model.tileLeads)
        let order = model.tileOrder.flatMap { $0.arrangement.order.isEmpty ? nil : $0 }
        let tiles = order?.tiles(projects, limit: Self.limit(sizeClass)) ?? ranked
        let arranged = (tiles.shown + tiles.more).map(\.id)
        if !tiles.shown.isEmpty {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(alignment: .top, spacing: 8) {
                    ForEach(tiles.shown, id: \.id) { project in
                        let count = counts[project.id] ?? 0
                        let pinned = model.tileOrder?.isPinned(project.id) == true
                        face(project.displayName, count: count, pinned: pinned) { letter(project) }
                            .onTapGesture { open(ConversationRoute.project(project.id)) }
                            .onLongPressGesture(minimumDuration: 0.4) { if model.tileOrder != nil { arranging = project } }
                            .accessibilityElement(children: .ignore)
                            .accessibilityAddTraits(.isButton)
                            .accessibilityAction { open(ConversationRoute.project(project.id)) }
                            .accessibilityLabel("Project \(project.displayName)")
                            .accessibilityValue([pinned ? "Pinned" : nil, count > 0 ? "\(count) waiting" : nil].compactMap { $0 }.joined(separator: ", "))
                            .accessibilityIdentifier("project-tile-" + project.id)
                            .accessibilityActions { arrange(project.id, arranged: arranged) }
                    }
                    if !tiles.more.isEmpty {
                        Menu {
                            ForEach(tiles.more, id: \.id) { project in
                                Button(project.archived == true ? "\(project.displayName) (archived)" : project.displayName) {
                                    open(ConversationRoute.project(project.id))
                                }
                            }
                        } label: {
                            face("+\(tiles.more.count)", count: tiles.more.reduce(0) { $0 + (counts[$1.id] ?? 0) }) {
                                Image(systemName: "ellipsis").font(.body.weight(.medium)).foregroundStyle(.primary)
                                    .frame(width: mark, height: mark)
                                    .background(.quaternary, in: RoundedRectangle(cornerRadius: 12))
                            }
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(Self.moreTitle(tiles.more.count))
                        .accessibilityIdentifier("project-tile-more")
                    }
                }
                .padding(.vertical, 6)
            }
            .scrollClipDisabled()
            .accessibilityElement(children: .contain)
            .accessibilityLabel("Projects")
            .sheet(item: $arranging) { project in
                arrangeSheet(project, arranged: arranged)
            }
        }
    }

    private func arrangeSheet(_ project: ProjectState, arranged: [String]) -> some View {
        VStack(spacing: 10) {
            Text(project.displayName)
                .font(.headline)
                .lineLimit(1)
                .padding(.bottom, 4)
            if let order = model.tileOrder {
                let pinned = order.isPinned(project.id)
                sheetButton(pinned ? "Unpin" : "Pin to Front", systemImage: pinned ? "pin.slash" : "pin") { order.change(.pin(project.id, !pinned), arranged: arranged) }
                if order.moves(.step(project.id, by: -1), arranged: arranged) {
                    sheetButton("Move Earlier", systemImage: "arrow.left") { order.change(.step(project.id, by: -1), arranged: arranged) }
                }
                if order.moves(.step(project.id, by: 1), arranged: arranged) {
                    sheetButton("Move Later", systemImage: "arrow.right") { order.change(.step(project.id, by: 1), arranged: arranged) }
                }
            }
        }
        .buttonStyle(.borderedProminent)
        .tint(PhonePalette.prominent)
        .controlSize(.large)
        .padding(20)
        .presentationDetents([.height(280)])
        .presentationDragIndicator(.visible)
    }

    private func sheetButton(_ title: String, systemImage: String, action: @escaping () -> Void) -> some View {
        Button {
            action()
            arranging = nil
        } label: {
            Label(title, systemImage: systemImage).frame(maxWidth: .infinity).foregroundStyle(PhonePalette.onAccent)
        }
    }

    @ViewBuilder
    private func arrange(_ id: String, arranged: [String]) -> some View {
        if let order = model.tileOrder {
            let pinned = order.isPinned(id)
            Button(pinned ? "Unpin" : "Pin to Front", systemImage: pinned ? "pin.slash" : "pin") { order.change(.pin(id, !pinned), arranged: arranged) }
            if order.moves(.step(id, by: -1), arranged: arranged) {
                Button("Move Earlier", systemImage: "arrow.left") { order.change(.step(id, by: -1), arranged: arranged) }
            }
            if order.moves(.step(id, by: 1), arranged: arranged) {
                Button("Move Later", systemImage: "arrow.right") { order.change(.step(id, by: 1), arranged: arranged) }
            }
        }
    }

    private func letter(_ project: ProjectState) -> some View {
        let colour = PhonePalette.project(model.colourID(of: project.id))
        return Text(ProjectTiles.letter(project.displayName))
            .font(.body.weight(.semibold))
            .foregroundStyle(colour.ink)
            .frame(width: mark, height: mark)
            .background(colour.fill, in: RoundedRectangle(cornerRadius: 12))
    }

    private func face(_ title: String, count: Int, pinned: Bool = false, @ViewBuilder icon: () -> some View) -> some View {
        VStack(spacing: 4) {
            icon()
                .overlay(alignment: .topLeading) {
                    if pinned {
                        Image(systemName: "pin.fill")
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(.secondary)
                            .padding(3)
                            .background(PhonePalette.ground, in: Circle())
                            .offset(x: -7, y: -6)
                    }
                }
                .overlay(alignment: .topTrailing) {
                    if count > 0 {
                        Text("\(count)")
                            .font(.caption2.weight(.semibold))
                            .monospacedDigit()
                            .foregroundStyle(PhonePalette.attention)
                            .padding(.horizontal, 5)
                            .padding(.vertical, 1)
                            .background(PhonePalette.ground, in: Capsule())
                            .offset(x: 8, y: -6)
                    }
                }
            if !typeSize.isAccessibilitySize {
                Text(title)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
            }
        }
        .frame(width: max(64, mark + 16))
        .contentShape(Rectangle())
    }
}
