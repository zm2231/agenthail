import SwiftUI

struct IOSSessionFilterMenu: View {
    @Bindable var model: AgenthailIOSModel
    let surfaces: [String]

    var body: some View {
        Menu {
            Picker("Status", selection: $model.refinement.status) {
                Text("All statuses").tag(SessionStatusFilter?.none)
                ForEach(SessionStatusFilter.allCases) { Text($0.label).tag(Optional($0)) }
            }
            .pickerStyle(.inline)
            Picker("Surface", selection: $model.refinement.surface) {
                Text("All surfaces").tag(String?.none)
                ForEach(surfaces, id: \.self) { Text(SessionRefinement.surfaceLabel($0)).tag(Optional($0)) }
            }
            .pickerStyle(.inline)
            if model.refinement.isActive {
                Divider()
                Button("Clear Filters", systemImage: "xmark.circle") { model.refinement = SessionRefinement() }
            }
        } label: {
            Image(systemName: model.refinement.isActive ? "line.3.horizontal.decrease.circle.fill" : "line.3.horizontal.decrease.circle")
        }
        .accessibilityLabel("Filter sessions")
        .accessibilityValue(model.refinement.isActive ? "Filtered" : "")
        .accessibilityIdentifier("filter-menu")
    }
}

struct IOSSessionFilterChips: View {
    var model: AgenthailIOSModel

    var body: some View {
        let refinement = model.refinement
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                if let status = refinement.status {
                    chip(status.label, id: "status") { model.refinement.status = nil }
                }
                if let surface = refinement.surface {
                    chip(SessionRefinement.surfaceLabel(surface), id: "surface") { model.refinement.surface = nil }
                }
            }
            .padding(.vertical, 2)
        }
        .scrollClipDisabled()
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Filters")
    }

    private func chip(_ title: String, id: String, remove: @escaping () -> Void) -> some View {
        Button(action: remove) {
            HStack(spacing: 6) {
                Text(title)
                Image(systemName: "xmark").font(.caption2.weight(.bold))
            }
            .font(.footnote.weight(.semibold))
            .foregroundStyle(Color.primary)
            .padding(.horizontal, 12)
            .frame(minHeight: 44)
            .floatingGlass(in: Capsule(), interactive: true)
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Remove filter: \(title)")
        .accessibilityIdentifier("filter-chip-" + id)
    }
}
