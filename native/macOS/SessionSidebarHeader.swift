import SwiftUI

/// A fixed-size row here widens the split view column past its maximum and clips it in narrow windows.
struct SessionSidebarHeader: View {
    @ObservedObject var model: AgenthailModel
    let counts: [SessionFilter: Int]
    let help: (SessionFilter) -> String
    let newSessionHelp: String
    let findShortcut: KeyboardShortcut?
    let addProject: () -> Void
    @FocusState private var searchFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            ViewThatFits(in: .horizontal) {
                filters(counted: true, spacing: DesktopSpace.m)
                filters(counted: true, spacing: DesktopSpace.s)
                filters(counted: false, spacing: DesktopSpace.m)
                filters(counted: false, spacing: DesktopSpace.s)
                filterMenu
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .font(.system(size: DesktopType.body))
            .padding(.horizontal, DesktopSpace.l)
            .padding(.top, DesktopSpace.xs)
            .padding(.bottom, DesktopSpace.s)
            .scrimText()

            HStack(spacing: DesktopSpace.s) {
                search
                SessionRefinementMenu(model: model)
                Button(action: addProject) {
                    Image(systemName: "folder.badge.plus")
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .help("Add project")
                .accessibilityLabel("Add project")
                Button {
                    model.openNewSession()
                } label: {
                    Image(systemName: "square.and.pencil")
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.text2)
                .help(newSessionHelp)
                .accessibilityLabel("New session")
            }
            .font(.system(size: DesktopType.body))
            .padding(.leading, DesktopSpace.m)
            .padding(.trailing, DesktopSpace.l)
            .padding(.bottom, 10)
            .scrimText()
        }
    }

    private func filters(counted: Bool, spacing: CGFloat) -> some View {
        HStack(spacing: spacing) {
            ForEach(SessionFilter.allCases) { filter in
                Button {
                    model.sessionFilter = filter
                } label: {
                    HStack(spacing: DesktopSpace.xs) {
                        Text(filter.rawValue)
                            .fontWeight(model.sessionFilter == filter ? .semibold : .regular)
                            .foregroundStyle(model.sessionFilter == filter ? DesktopPalette.text : DesktopPalette.text2)
                        if counted {
                            Text("\(counts[filter] ?? 0)")
                                .font(.system(size: DesktopType.caption))
                                .foregroundStyle(DesktopPalette.text2)
                        }
                    }
                    .lineLimit(1)
                    .fixedSize()
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(model.sessionFilter == filter ? .isSelected : [])
                .accessibilityValue("\(counts[filter] ?? 0) sessions")
                .help(help(filter))
            }
        }
    }

    private var filterMenu: some View {
        Menu {
            Picker("Show", selection: $model.sessionFilter) {
                ForEach(SessionFilter.allCases) { filter in
                    Text("\(filter.rawValue) \(counts[filter] ?? 0)").tag(filter)
                }
            }
            .pickerStyle(.inline)
        } label: {
            Text("\(model.sessionFilter.rawValue) \(counts[model.sessionFilter] ?? 0)")
                .fontWeight(.semibold)
                .foregroundStyle(DesktopPalette.text)
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
        .help(help(model.sessionFilter))
        .accessibilityLabel("Session filter")
    }

    private var search: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: DesktopType.caption))
                .foregroundStyle(DesktopPalette.text2)
            TextField("Search sessions", text: Binding(get: { model.searchQuery }, set: { model.search($0) }))
                .textFieldStyle(.plain)
                .font(.system(size: DesktopType.body))
                .focused($searchFocused)
                .onKeyPress(.escape) {
                    model.search("")
                    searchFocused = false
                    return .handled
                }
            if !model.searchQuery.isEmpty {
                Button {
                    model.search("")
                } label: {
                    Image(systemName: "xmark.circle.fill")
                }
                .buttonStyle(.plain)
                .foregroundStyle(DesktopPalette.muted)
                .accessibilityLabel("Clear search")
            }
        }
        .padding(.horizontal, 9)
        .padding(.vertical, 6)
        .background(DesktopPalette.fill, in: RoundedRectangle(cornerRadius: DesktopRadius.control))
        .background {
            Button("") { searchFocused = true }
                .keyboardShortcut(findShortcut)
                .hidden()
        }
    }
}
