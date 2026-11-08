import SwiftUI

struct PhoneProjectTiles: View {
    @ObservedObject var model: AgenthailIOSModel
    let open: (String) -> Void
    @Environment(\.horizontalSizeClass) private var sizeClass
    @Environment(\.dynamicTypeSize) private var typeSize
    @ScaledMetric(relativeTo: .body) private var mark: CGFloat = 44

    static func limit(_ sizeClass: UserInterfaceSizeClass?) -> Int { sizeClass == .regular ? 6 : 4 }

    static func moreTitle(_ count: Int) -> String { count == 1 ? "1 more project" : "\(count) more projects" }

    var body: some View {
        let projects = model.qualifiedProjects
        let counts = model.decisionFeed.inbox.needsYou(in: projects)
        let tiles = ProjectTiles.order(projects, limit: Self.limit(sizeClass), waiting: counts, leads: model.tileLeads)
        if !tiles.shown.isEmpty {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(alignment: .top, spacing: 8) {
                    ForEach(tiles.shown, id: \.id) { project in
                        let count = counts[project.id] ?? 0
                        Button { open(ConversationRoute.project(project.id)) } label: {
                            face(project.displayName, count: count) { letter(project) }
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Project \(project.displayName)")
                        .accessibilityValue(count > 0 ? "\(count) waiting" : "")
                        .accessibilityIdentifier("project-tile-" + project.id)
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

    private func face(_ title: String, count: Int, @ViewBuilder icon: () -> some View) -> some View {
        VStack(spacing: 4) {
            icon()
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
