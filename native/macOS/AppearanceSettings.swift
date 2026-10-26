import AppKit
import SwiftUI
import UniformTypeIdentifiers

struct AppearanceSettings: View {
    @ObservedObject var model: AgenthailModel
    @ObservedObject var interface: InterfaceStore
    @StateObject private var thumbnails = BackgroundThumbnails()
    @Environment(\.colorScheme) private var colorScheme
    @State private var draft = AppearanceDraft(device: InterfaceStore.device)
    @State private var presets: [UIPreset] = []
    @State private var error: String?
    @State private var editingMesh = false
    @State private var confirmingReset = false
    @State private var dropTargeted = false

    init(model: AgenthailModel) {
        self.model = model
        self.interface = model.interface
    }

    private var config: UIConfig { interface.config }
    private var dark: Bool { colorScheme == .dark }
    private var chosenBackground: String { config.chosen["background"] ?? "" }

    static let accents = ["#4f8ff7", "#3fb68b", "#e9a23b", "#e5534b", "#a371f7", "#e86ba5"]

    var body: some View {
        Form {
            Section {
                Picker("Apply changes to", selection: $draft.scope) {
                    Text("All devices").tag(AppearanceDraft.Scope.everywhere)
                    Text("This Mac only").tag(AppearanceDraft.Scope.thisMac)
                }
                .pickerStyle(.segmented)
                row("theme", "Appearance") {
                    Picker("Appearance", selection: choice("theme", label: "appearance")) {
                        Text("System").tag("system")
                        Text("Light").tag("light")
                        Text("Dark").tag("dark")
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    .fixedSize()
                }
                row("theme.accent", "Accent") { accentPicker }
            }
            Section("Background") {
                backgroundGrid
                row("background.dim", "Dim") { slider("background.dim", label: "background dim", range: 0...0.8) }
                row("background.opacity", "Strength on empty views") { slider("background.opacity", label: "background strength", range: 0...1) }
                row("background.session-opacity", "Strength in a session") { slider("background.session-opacity", label: "background strength in a session", range: 0...1) }
                if chosenBackground.hasPrefix("image:") {
                    row("background.fit", "Fit") {
                        Picker("Fit", selection: choice("background.fit", label: "image fit")) {
                            ForEach(["cover", "contain", "center", "tile"], id: \.self) { Text($0.capitalized).tag($0) }
                        }
                        .labelsHidden()
                        .fixedSize()
                    }
                }
            }
            Section("Glass") {
                row("glass", "Translucent panes") {
                    Toggle("Translucent panes", isOn: toggle("glass", label: "translucent panes")).labelsHidden().toggleStyle(.switch)
                }
                Group {
                    row("glass.blur", "Blur") { slider("glass.blur", label: "glass blur", range: 0...64, unit: " pt") }
                    row("glass.sidebar.opacity", "Sidebar tint") { slider("glass.sidebar.opacity", label: "sidebar tint", range: 0...1) }
                    row("glass.opacity", "Content tint") { slider("glass.opacity", label: "content tint", range: 0...1) }
                    row("glass.composer.opacity", "Floating controls tint") { slider("glass.composer.opacity", label: "floating controls tint", range: 0...1) }
                    row("glass.tint", "Tint colour") {
                        Picker("Tint colour", selection: choice("glass.tint", label: "glass tint")) {
                            Text("Window").tag("ground")
                            Text("Accent").tag("accent")
                        }
                        .pickerStyle(.segmented)
                        .labelsHidden()
                        .fixedSize()
                    }
                    row("glass.readability", "Keep text readable") {
                        Toggle("Keep text readable", isOn: toggle("glass.readability", label: "text readability", on: "auto")).labelsHidden().toggleStyle(.switch)
                    }
                }
                .disabled(!config.glass)
            }
            Section("Texture") {
                row("texture", "Texture") {
                    Picker("Texture", selection: choice("texture", label: "texture")) {
                        Text("None").tag("none")
                        Text("Grain").tag("grain")
                        Text("Scanlines").tag("scanlines")
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                    .fixedSize()
                }
                row("texture.strength", "Strength") { slider("texture.strength", label: "texture strength", range: 0...0.6) }
                    .disabled(config.texture == "none")
            }
            Section { historyRows }
            if let error {
                Label(error, systemImage: "exclamationmark.triangle")
                    .font(.system(size: DesktopType.secondary))
                    .foregroundStyle(DesktopPalette.failed)
                    .textSelection(.enabled)
            }
        }
        .formStyle(.grouped)
        .onDrop(of: [.fileURL], isTargeted: $dropTargeted) { providers in importDropped(providers) }
        .sheet(isPresented: $editingMesh) {
            MeshEditor(initial: MeshEditor.colors(from: config.background(dark: dark), presets: presets, dark: dark)) { colors in
                editingMesh = false
                if let colors { write(draft.set("background", AppearanceDraft.mesh(colors), label: "a custom background")) }
            }
        }
        .confirmationDialog("Reset appearance to the defaults?", isPresented: $confirmingReset) {
            Button("Reset", role: .destructive) { write(draft.reset()) }
        } message: {
            Text(draft.scope == .thisMac ? "This removes every appearance setting made for this Mac only." : "This removes every appearance setting that applies to all devices, and this Mac's own settings that would hide the reset. Other devices keep their own settings.")
        }
        .onDisappear { AccentPanel.shared.dismiss() }
        .task {
            guard let api = model.api else { return }
            presets = (try? await api.uiPresets()) ?? []
            await interface.loadHistory(api: api)
        }
    }

    private var accentPicker: some View {
        HStack(spacing: DesktopSpace.s) {
            swatch(selected: config.text("theme.accent", "system") == "system", help: "System accent") {
                Circle().fill(AngularGradient(colors: [.red, .yellow, .green, .blue, .purple, .red], center: .center))
            } action: { write(draft.set("theme.accent", "system", label: "accent to the system accent")) }
            ForEach(Self.accents, id: \.self) { hex in
                swatch(selected: config.text("theme.accent", "system") == hex, help: hex) {
                    Circle().fill(color(hex))
                } action: { write(draft.set("theme.accent", hex, label: "accent to \(hex)")) }
            }
            let current = config.text("theme.accent", "system")
            let custom = current != "system" && !Self.accents.contains(current)
            swatch(selected: custom, help: "Any colour") {
                Circle()
                    .fill(custom ? AnyShapeStyle(color(current)) : AnyShapeStyle(DesktopPalette.fill))
                    .overlay(Circle().strokeBorder(DesktopPalette.text2.opacity(custom ? 0 : 0.6), lineWidth: 1))
                    .overlay {
                        if !custom { Image(systemName: "plus").font(.system(size: 9, weight: .bold)).foregroundStyle(DesktopPalette.text) }
                    }
            } action: {
                AccentPanel.shared.open(start: config.accent.map { NSColor(srgbRed: $0.red, green: $0.green, blue: $0.blue, alpha: 1) }) { picked in
                    guard let rgb = picked.usingColorSpace(.sRGB) else { return }
                    let hex = AppearanceDraft.hex(red: rgb.redComponent, green: rgb.greenComponent, blue: rgb.blueComponent)
                    guard hex != config.text("theme.accent", "system") else { return }
                    write(draft.set("theme.accent", hex, label: "accent to \(hex)"))
                }
            }
        }
    }

    private func swatch<Fill: View>(selected: Bool, help: String, @ViewBuilder fill: () -> Fill, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            fill()
                .frame(width: 18, height: 18)
                .padding(2)
                .overlay(Circle().strokeBorder(DesktopPalette.text.when(selected), lineWidth: 1.5))
        }
        .buttonStyle(.plain)
        .help(help)
    }

    private var backgroundGrid: some View {
        LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: DesktopSpace.m), count: 5), spacing: DesktopSpace.m) {
            ForEach(presets) { preset in
                tile(preset.name, selected: chosenBackground == preset.value) {
                    BackgroundSwatch(background: preset.background(dark: dark), thumbnails: thumbnails, api: model.api)
                } action: { write(draft.set("background", preset.value, label: "background to \(preset.name)")) }
            }
            tile("Desktop", selected: chosenBackground == "desktop") {
                BackgroundSwatch(background: .desktop, thumbnails: thumbnails, api: model.api)
            } action: { write(draft.set("background", "desktop", label: "background to the desktop picture")) }
            tile("None", selected: chosenBackground == "none") {
                BackgroundSwatch(background: .none, thumbnails: thumbnails, api: model.api)
            } action: { write(draft.set("background", "none", label: "no background")) }
            tile("Image…", selected: chosenBackground.hasPrefix("image:")) {
                if case .image = config.background(dark: dark), chosenBackground.hasPrefix("image:") {
                    BackgroundSwatch(background: config.background(dark: dark), thumbnails: thumbnails, api: model.api)
                } else {
                    ActionSwatch(symbol: "photo.badge.plus", caption: "or drop", highlighted: dropTargeted)
                }
            } action: { chooseImage() }
            tile("Custom…", selected: chosenBackground.hasPrefix("mesh:") || chosenBackground.hasPrefix("gradient: ") && !presets.contains { $0.value == chosenBackground }) {
                ActionSwatch(symbol: "paintpalette", caption: nil, highlighted: false)
            } action: { editingMesh = true }
        }
        .padding(.vertical, DesktopSpace.xs)
    }

    private func tile<Fill: View>(_ name: String, selected: Bool, @ViewBuilder fill: () -> Fill, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(spacing: DesktopSpace.xs) {
                fill()
                    .frame(height: 52)
                    .frame(maxWidth: .infinity)
                    .clipShape(RoundedRectangle(cornerRadius: DesktopRadius.control + 2))
                    .overlay(RoundedRectangle(cornerRadius: DesktopRadius.control + 2).strokeBorder(selected ? AnyShapeStyle(DesktopPalette.accent) : AnyShapeStyle(DesktopPalette.line), lineWidth: selected ? 2.5 : 1))
                Text(name)
                    .font(.system(size: DesktopType.caption, weight: selected ? .semibold : .regular))
                    .foregroundStyle(selected ? DesktopPalette.text : DesktopPalette.text2)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    @ViewBuilder
    private var historyRows: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 2) {
                if let last = interface.history.first {
                    Text(last.message).lineLimit(1)
                    Text(describe(last)).font(.system(size: DesktopType.secondary)).foregroundStyle(DesktopPalette.text2)
                } else {
                    Text("No changes yet").foregroundStyle(DesktopPalette.text2)
                }
            }
            Spacer()
            Button("Undo") { write(UIChange(action: "undo")) }
                .disabled(interface.history.count < 2)
            Button("Reset to Default…") { confirmingReset = true }
        }
        if interface.history.count > 1 {
            DisclosureGroup("History") {
                ForEach(interface.history.dropLast()) { entry in
                    HStack(alignment: .firstTextBaseline) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(entry.message).lineLimit(1)
                            Text(describe(entry)).font(.system(size: DesktopType.caption)).foregroundStyle(DesktopPalette.text2)
                        }
                        Spacer()
                        Button("Undo") { write(UIChange(action: "undo", rev: entry.rev)) }
                            .controlSize(.small)
                    }
                }
            }
        }
    }

    private func describe(_ entry: UIChangeEntry) -> String {
        let who = entry.author == "person" ? "You" : entry.author
        guard let date = entry.date else { return who }
        return "\(who) · \(date.formatted(.relative(presentation: .named)))"
    }

    private func row<Control: View>(_ key: String, _ label: String, @ViewBuilder control: () -> Control) -> some View {
        LabeledContent {
            HStack(spacing: DesktopSpace.s) {
                control()
                if let owner = draft.override(key, in: config), draft.scope == .everywhere || owner != "This Mac" {
                    Button {
                        if let change = draft.clearOverride(key, in: config) { write(change) }
                    } label: {
                        Label(owner, systemImage: "arrow.uturn.backward")
                            .font(.system(size: DesktopType.caption))
                    }
                    .buttonStyle(.borderless)
                    .help("\(owner) has its own value for this. Click to use the setting for all devices.")
                }
            }
        } label: {
            Text(label)
        }
    }

    private func slider(_ key: String, label: String, range: ClosedRange<Double>, unit: String = "") -> some View {
        let value = Binding<Double> {
            config.number(key, range.lowerBound)
        } set: { interface.preview(key, .number($0)) }
        return HStack {
            Slider(value: value, in: range) { editing in
                guard !editing else { return }
                write(draft.set(key, AppearanceDraft.format(value.wrappedValue), label: "\(label) to \(AppearanceDraft.format(value.wrappedValue))"))
            }
            Text(AppearanceDraft.format(value.wrappedValue) + unit)
                .monospacedDigit()
                .foregroundStyle(DesktopPalette.text2)
                .frame(width: 48, alignment: .trailing)
        }
        .frame(minWidth: 220)
    }

    private func choice(_ key: String, label: String) -> Binding<String> {
        Binding {
            config.text(key, "")
        } set: { value in
            guard value != config.text(key, "") else { return }
            write(draft.set(key, value, label: "\(label) to \(value)"))
        }
    }

    private func toggle(_ key: String, label: String, on: String = "on") -> Binding<Bool> {
        Binding {
            config.text(key, on) == on
        } set: { value in
            write(draft.set(key, value ? on : "off", label: "\(label) \(value ? "on" : "off")"))
        }
    }

    private func write(_ change: UIChange) {
        guard let api = model.api else {
            interface.dropPreview()
            error = "Agenthail isn't connected."
            return
        }
        Task { error = await interface.change(change, api: api) }
    }

    private func chooseImage() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.image]
        panel.allowsMultipleSelection = false
        panel.message = "Choose a background image"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        importImage(url)
    }

    private func importDropped(_ providers: [NSItemProvider]) -> Bool {
        guard let provider = providers.first(where: { $0.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) }) else { return false }
        _ = provider.loadObject(ofClass: URL.self) { url, _ in
            guard let url else { return }
            Task { @MainActor in importImage(url) }
        }
        return true
    }

    private func importImage(_ url: URL) {
        guard let api = model.api else {
            error = "Agenthail isn't connected."
            return
        }
        Task {
            do {
                let path = try await api.uploadBackground(file: url)
                error = await interface.change(draft.set("background", "image: \(path)", label: "background to \(url.lastPathComponent)"), api: api)
            } catch {
                self.error = AgenthailAPIError.userMessage(error)
            }
        }
    }

    private func color(_ hex: String) -> Color {
        let c = UIConfig.RGBColor(hex: hex)!
        return Color(red: c.red, green: c.green, blue: c.blue)
    }
}

/// Sends one pick when the colour panel closes, not one write per drag.
@MainActor
final class AccentPanel: NSObject, NSWindowDelegate {
    static let shared = AccentPanel()
    private var pick = PendingPick<NSColor>()

    func open(start: NSColor?, picked: @escaping (NSColor) -> Void) {
        pick.begin(picked)
        let panel = NSColorPanel.shared
        panel.showsAlpha = false
        if let start { panel.color = start }
        panel.setTarget(self)
        panel.setAction(#selector(colourChanged(_:)))
        panel.delegate = self
        panel.makeKeyAndOrderFront(nil)
    }

    /// Called when the pane goes away: the pick is dropped and the panel closed.
    func dismiss() {
        guard NSColorPanel.shared.delegate === self else { return }
        pick.cancel()
        NSColorPanel.shared.close()
    }

    @objc private func colourChanged(_ sender: NSColorPanel) {
        pick.markChanged()
    }

    func windowWillClose(_ notification: Notification) {
        let panel = NSColorPanel.shared
        panel.setTarget(nil)
        panel.setAction(nil)
        panel.delegate = nil
        pick.finish()?(panel.color)
    }
}

/// Loads small copies of background images for the picker.
@MainActor
final class BackgroundThumbnails: ObservableObject {
    @Published private(set) var images: [String: NSImage] = [:]
    private var loading: Set<String> = []

    static let desktopKey = "desktop:"

    func image(_ path: String, api: AgenthailAPI?) -> NSImage? {
        if let image = images[path] { return image }
        guard !loading.contains(path) else { return nil }
        loading.insert(path)
        Task {
            let data: Data?
            if path == Self.desktopKey {
                data = NSScreen.main.flatMap { NSWorkspace.shared.desktopImageURL(for: $0) }.flatMap { try? Data(contentsOf: $0) }
            } else {
                data = try? await api?.uiBackground(path: path)
            }
            if let data, let image = await Task.detached(operation: { WallpaperImage.decode(data, id: path, maxPixels: 320) }).value {
                images[path] = NSImage(cgImage: image.image, size: .zero)
            }
            loading.remove(path)
        }
        return nil
    }
}

struct BackgroundSwatch: View {
    let background: UIConfig.Background
    @ObservedObject var thumbnails: BackgroundThumbnails
    let api: AgenthailAPI?

    var body: some View {
        switch background {
        case .none:
            DesktopPalette.window
                .overlay(Image(systemName: "circle.slash").foregroundStyle(DesktopPalette.text2))
        case .desktop:
            picture(BackgroundThumbnails.desktopKey)
                .overlay(alignment: .bottomTrailing) {
                    Image(systemName: "menubar.dock.rectangle")
                        .font(.system(size: 10))
                        .foregroundStyle(.white)
                        .padding(4)
                        .shadow(radius: 2)
                }
        case .image(let path):
            picture(path)
        case .linear(let angle, let stops):
            LinearGradient(stops: stops.map { Gradient.Stop(color: Self.color($0.color), location: $0.location) }, startPoint: Self.start(angle), endPoint: Self.end(angle))
        case .mesh(let columns, let rows, let colors):
            MeshGradient(width: columns, height: rows, points: Self.points(columns: columns, rows: rows), colors: colors.map(Self.color))
        }
    }

    private func picture(_ path: String) -> some View {
        Group {
            if let image = thumbnails.image(path, api: api) {
                Image(nsImage: image).resizable().aspectRatio(contentMode: .fill)
            } else {
                DesktopPalette.fill
            }
        }
    }

    static func color(_ rgb: UIConfig.RGBColor) -> Color {
        Color(red: rgb.red, green: rgb.green, blue: rgb.blue)
    }

    static func points(columns: Int, rows: Int) -> [SIMD2<Float>] {
        (0..<rows).flatMap { row in
            (0..<columns).map { column in SIMD2(Float(column) / Float(columns - 1), Float(row) / Float(rows - 1)) }
        }
    }

    static func start(_ angle: Double) -> UnitPoint {
        let radians = (angle - 90) * .pi / 180
        return UnitPoint(x: 0.5 - cos(radians) / 2, y: 0.5 - sin(radians) / 2)
    }

    static func end(_ angle: Double) -> UnitPoint {
        let radians = (angle - 90) * .pi / 180
        return UnitPoint(x: 0.5 + cos(radians) / 2, y: 0.5 + sin(radians) / 2)
    }
}

struct ActionSwatch: View {
    let symbol: String
    let caption: String?
    let highlighted: Bool

    var body: some View {
        ZStack {
            DesktopPalette.fill
            RoundedRectangle(cornerRadius: DesktopRadius.control + 2)
                .strokeBorder(highlighted ? AnyShapeStyle(DesktopPalette.accent) : AnyShapeStyle(DesktopPalette.text2.opacity(0.6)), style: StrokeStyle(lineWidth: 1.5, dash: [4, 3]))
                .padding(3)
            VStack(spacing: 2) {
                Image(systemName: symbol).font(.system(size: 16, weight: .medium))
                if let caption { Text(caption).font(.system(size: DesktopType.caption)) }
            }
            .foregroundStyle(highlighted ? DesktopPalette.accent : DesktopPalette.text)
        }
    }
}

struct MeshEditor: View {
    @State var colors: [Color]
    let done: ([String]?) -> Void

    init(initial: [String], done: @escaping ([String]?) -> Void) {
        _colors = State(initialValue: initial.compactMap(UIConfig.RGBColor.init(hex:)).map(BackgroundSwatch.color))
        self.done = done
    }

    static func colors(from background: UIConfig.Background, presets: [UIPreset], dark: Bool) -> [String] {
        if case .mesh(3, 3, let colors) = background {
            return colors.map { AppearanceDraft.hex(red: $0.red, green: $0.green, blue: $0.blue) }
        }
        if let preset = presets.first(where: { $0.value.hasPrefix("gradient: ") }), case .mesh(3, 3, let colors) = preset.background(dark: dark) {
            return colors.map { AppearanceDraft.hex(red: $0.red, green: $0.green, blue: $0.blue) }
        }
        return Array(repeating: dark ? "#1b2230" : "#d8dde8", count: 9)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: DesktopSpace.l) {
            Text("Custom background").font(.headline)
            MeshGradient(width: 3, height: 3, points: BackgroundSwatch.points(columns: 3, rows: 3), colors: colors)
                .frame(width: 360, height: 200)
                .clipShape(RoundedRectangle(cornerRadius: DesktopRadius.sheet))
                .overlay {
                    Grid(horizontalSpacing: 0, verticalSpacing: 0) {
                        ForEach(0..<3, id: \.self) { row in
                            GridRow {
                                ForEach(0..<3, id: \.self) { column in
                                    ColorPicker("Point \(row * 3 + column + 1)", selection: $colors[row * 3 + column], supportsOpacity: false)
                                        .labelsHidden()
                                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                                }
                            }
                        }
                    }
                }
            Text("Each well sets the colour at that point of the mesh.")
                .font(.system(size: DesktopType.secondary))
                .foregroundStyle(DesktopPalette.text2)
            HStack {
                Spacer()
                Button("Cancel") { done(nil) }.keyboardShortcut(.cancelAction)
                Button("Use Background") {
                    done(colors.map { color in
                        let rgb = NSColor(color).usingColorSpace(.sRGB) ?? .black
                        return AppearanceDraft.hex(red: rgb.redComponent, green: rgb.greenComponent, blue: rgb.blueComponent)
                    })
                }
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding(DesktopSpace.xl)
    }
}
