import SwiftUI

struct IOSProjectSection<Rows: View>: View {
    var model: AgenthailIOSModel
    let project: SessionTree.Project
    let selectedID: String?
    @Binding var expandedProjects: Set<String>
    @Binding var sheet: ProjectSheet?
    @Binding var confirmation: ProjectConfirmation?
    @Binding var place: NewSessionPlace?
    @Binding var messaging: ListedProject?
    let openSession: (String) -> Void
    @ViewBuilder let rows: (SessionFamily) -> Rows

    var body: some View {
        let expanded = expandedProjects.contains(project.id)
        let single = project.checkouts.count == 1 ? project.checkouts.first : nil
        Section {
            ForEach(project.shown(expanded: expanded, keeping: selectedID).checkouts) { checkout in
                if single == nil {
                    IOSCheckoutRow(checkout: checkout, host: project.spansHosts && model.showsHostNames ? project.hostLabel(of: checkout) : nil)
                }
                ForEach(checkout.families) { family in rows(family) }
            }
            if let more = project.moreLabel(expanded: expanded, keeping: selectedID) {
                Button(more) {
                    if expanded { expandedProjects.remove(project.id) } else { expandedProjects.insert(project.id) }
                }
                .font(.footnote)
                .foregroundStyle(PhonePalette.accentText)
                .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                .listRowBackground(GlassRowBackground(selected: false))
                .accessibilityIdentifier("more-" + project.id)
            }
        } header: {
            header(single)
        }
    }

    private func header(_ single: SessionTree.Checkout?) -> some View {
        HStack(spacing: 10) {
            HStack(spacing: 8) {
                Image(systemName: project.identity == nil ? "tray" : project.archived ? "archivebox" : "folder")
                Text(project.name)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                    .layoutPriority(1)
                if let branch = single?.branchLabel {
                    Text(branch)
                        .font(.caption.monospaced())
                        .foregroundStyle(PhonePalette.muted)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                if single?.dirty == true { IOSDirtyDot() }
                if let host = project.hostLabel, model.showsHostNames {
                    IOSHostBadge(name: host)
                }
                Spacer(minLength: 0)
                Text("\(project.counts.sessions)")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(PhonePalette.muted)
            }
            .glassLabel()
            .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
            .contentShape(Rectangle())
            .foregroundStyle(.primary)
            .textCase(nil)
            .accessibilityElement(children: .ignore)
            .accessibilityAddTraits(.isHeader)
            .contextMenu {
                if let identity = project.identity {
                    Button(project.archived ? "Unarchive Project" : "Archive Project", systemImage: "archivebox") {
                        Task { await model.setProjectArchived(identity.id, !project.archived) }
                    }
                    if let listed = model.projects[identity.id] {
                        IOSProjectMenuItems(model: model, project: listed, sheet: $sheet, confirmation: $confirmation, place: $place, messaging: $messaging)
                    }
                }
            }
            .accessibilityLabel(Self.spoken(project, single: single))
            .accessibilityValue(project.sessionCountLabel)
            .accessibilityIdentifier("project-" + project.id)
            if let identity = project.identity, model.projects[identity.id] != nil {
                openProjectButton(identity.id)
            }
        }
    }

    static func spoken(_ project: SessionTree.Project, single: SessionTree.Checkout?) -> String {
        var label = project.hostLabel.map { "Project \(project.name) on \($0)" } ?? "Project \(project.name)"
        if let branch = single?.branchLabel { label += ", branch \(branch)" }
        if single?.dirty == true { label += ", uncommitted changes" }
        return label
    }

    private func openProjectButton(_ projectID: String) -> some View {
        Button { openSession(ConversationRoute.project(projectID)) } label: {
            HStack(spacing: 2) {
                Text("Project")
                Image(systemName: "chevron.right")
            }
            .font(.caption.weight(.semibold))
            .foregroundStyle(PhonePalette.accentText)
            .frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .textCase(nil)
        .accessibilityLabel("Open project \(project.name)")
        .accessibilityIdentifier("open-project-" + projectID)
    }
}
