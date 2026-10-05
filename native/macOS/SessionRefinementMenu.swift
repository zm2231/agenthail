import SwiftUI

struct SessionRefinementMenu: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        Menu {
            Picker("Status", selection: $model.sessionRefinement.status) {
                Text("All statuses").tag(SessionStatusFilter?.none)
                ForEach(SessionStatusFilter.allCases) { Text($0.label).tag(Optional($0)) }
            }
            .pickerStyle(.inline)
            Picker("Surface", selection: $model.sessionRefinement.surface) {
                Text("All surfaces").tag(String?.none)
                ForEach(surfaces, id: \.self) { Text(SessionRefinement.surfaceLabel($0)).tag(Optional($0)) }
            }
            .pickerStyle(.inline)
            if model.sessionRefinement.isActive {
                Divider()
                Button("Clear Filters") { model.sessionRefinement = SessionRefinement() }
            }
        } label: {
            Image(systemName: model.sessionRefinement.isActive ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle")
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .fixedSize()
        .foregroundStyle(model.sessionRefinement.isActive ? DesktopPalette.accent : DesktopPalette.text2)
        .help("Filter by status and surface")
        .accessibilityLabel("Filter sessions")
    }

    private var surfaces: [String] {
        SessionRefinement.surfaces(model.knownSessions, configured: model.snapshot?.surfaces ?? [])
    }
}

struct SessionRefinementBar: View {
    @ObservedObject var model: AgenthailModel

    var body: some View {
        let refinement = model.sessionRefinement
        if refinement.isActive {
            HStack(spacing: 6) {
                if let status = refinement.status {
                    chip(status.label) { model.sessionRefinement.status = nil }
                }
                if let surface = refinement.surface {
                    chip(SessionRefinement.surfaceLabel(surface)) { model.sessionRefinement.surface = nil }
                }
                Spacer()
            }
            .padding(.horizontal, 12)
            .padding(.bottom, 8)
        }
    }

    private func chip(_ title: String, remove: @escaping () -> Void) -> some View {
        Button(action: remove) {
            HStack(spacing: 4) {
                Text(title)
                Image(systemName: "xmark").font(.system(size: 8, weight: .semibold))
            }
            .font(.system(size: 11.5))
            .foregroundStyle(DesktopPalette.text)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(DesktopPalette.selection, in: Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Remove \(title) filter")
    }
}
