import CryptoKit
import os
import SwiftUI
import UIKit

@MainActor
final class PhoneAppearance: ObservableObject {
    /// A transcript's pane scrims only around its rows; Settings and the Inbox scrim the whole pane.
    enum Surface: Hashable {
        case list
        case cards
        case pane
        case transcript
    }

    struct Key: Hashable {
        var surface: Surface
        var size: CGSize
        var scale: CGFloat
        var dark: Bool
        var solid: Bool
        var target: Float
        var readable = true

        func hash(into hasher: inout Hasher) {
            hasher.combine(surface)
            hasher.combine(size.width)
            hasher.combine(size.height)
            hasher.combine(scale)
            hasher.combine(dark)
            hasher.combine(solid)
            hasher.combine(target)
            hasher.combine(readable)
        }
    }

    @Published private(set) var config = UIConfig.defaults
    @Published private(set) var wallpapers: [String: WallpaperImage] = [:]
    @Published private(set) var rendered: [Key: BackdropImage] = [:]
    @Published private(set) var layered: [Key: BackdropLayers] = [:]
    @Published private(set) var extremes: [Key: Float] = [:]
    @Published private(set) var problem: String?
    private var pending: Set<Key> = []
    private var recent: [Key] = []
    private static let keptRenders = 6
    private var loadTask: Task<Void, Never>?
    private var loadGeneration = 0
    private let cache: URL?
    private static let signposter = OSSignposter(subsystem: "com.agenthail.ios", category: "Appearance")

    init(cache: URL? = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first?.appendingPathComponent("appearance", isDirectory: true)) {
        self.cache = cache
        if let cache { RenderStamp.prune(cache) }
        if let cache, let data = try? Data(contentsOf: cache.appendingPathComponent("ui.json")),
           let saved = try? JSONDecoder().decode(UIConfig.self, from: data) {
            config = saved
            let shown = (try? JSONDecoder().decode([String: String].self, from: Data(contentsOf: cache.appendingPathComponent("shown.json")))) ?? [:]
            let sources = saved.imagePaths.map { ($0, shown[$0] ?? $0) }
            let maxPixels = Self.maxPixels
            Task { [weak self] in
                let decoded = await Task.detached {
                    sources.compactMap { path, source -> (String, WallpaperImage)? in
                        guard let data = try? Data(contentsOf: cache.appendingPathComponent(Self.wallpaperFile(source))),
                              let image = WallpaperImage.decode(data, id: source, maxPixels: maxPixels) else { return nil }
                        return (path, image)
                    }
                }.value
                guard let self else { return }
                var filled = false
                for (path, image) in decoded where self.wallpapers[path] == nil && self.config.imagePaths.contains(path) {
                    self.wallpapers[path] = image
                    filled = true
                }
                if filled { self.forgetRenders() }
            }
        }
    }

    private static var maxPixels: Int {
        let screen = UIScreen.main.nativeBounds.size
        return Int(max(screen.width, screen.height))
    }

    static var deviceKind: String { UIDevice.current.userInterfaceIdiom == .pad ? "tablet" : "phone" }

    @Published private var chosenTextScale: Double?
    @Published private(set) var textScaleProblem: String?
    private var textScaleWrite: Task<Void, Never>?

    var textScale: Double { chosenTextScale ?? config.textScale }

    func setTextScale(_ scale: Double, api: AgenthailAPI?) {
        chosenTextScale = scale
        textScaleProblem = nil
        textScaleWrite?.cancel()
        guard let api else {
            textScaleProblem = "Not saved: no computer is connected."
            textScaleWrite = nil
            return
        }
        textScaleWrite = Task { [weak self] in
            try? await Task.sleep(for: .seconds(1))
            guard let self, !Task.isCancelled else { return }
            let percent = Int((scale * 100).rounded())
            let change = UIChange(action: "set", set: ["text.scale@device:\(UIDevice.current.name) = \(Double(percent) / 100)"], message: "Text size \(percent)%")
            do {
                _ = try await api.changeUI(change, device: UIDevice.current.name, kind: Self.deviceKind)
                guard !Task.isCancelled else { return }
                self.textScaleWrite = nil
                self.load(from: api)
            } catch {
                guard !Task.isCancelled else { return }
                self.textScaleWrite = nil
                self.chosenTextScale = nil
                self.textScaleProblem = "Text size wasn't saved. \(AgenthailAPIError.userMessage(error))"
            }
        }
    }

    var active: Bool {
        config.glass && (wallpaperSpec(dark: true) != nil || wallpaperSpec(dark: false) != nil)
    }

    var colorScheme: ColorScheme? {
        switch config.theme {
        case "dark": return .dark
        case "light": return .light
        default: return nil
        }
    }

    func load(from api: AgenthailAPI) {
        loadTask?.cancel()
        loadGeneration += 1
        let generation = loadGeneration
        loadTask = Task {
            let signpost = Self.signposter.makeSignpostID()
            let interval = Self.signposter.beginInterval("Appearance load", id: signpost)
            defer { Self.signposter.endInterval("Appearance load", interval) }
            do {
                let loaded = try await api.uiConfig(device: UIDevice.current.name, kind: Self.deviceKind)
                if Task.isCancelled || generation != loadGeneration { return }
                adopt(loaded)
                Self.signposter.emitEvent("Config applied", id: signpost)
                if textScaleWrite == nil { chosenTextScale = nil }
                var unreadable: [String] = []
                let active = Self.prefersDark(loaded)
                for path in Self.fetchOrder(loaded, dark: active) where wallpapers[path]?.id != path {
                    let data: Data
                    do {
                        data = try await api.uiBackground(path: path)
                    } catch {
                        if Task.isCancelled || error is CancellationError || (error as? URLError)?.code == .cancelled { return }
                        unreadable.append(path)
                        continue
                    }
                    let maxPixels = Self.maxPixels
                    let image = await Task.detached { WallpaperImage.decode(data, id: path, maxPixels: maxPixels) }.value
                    if Task.isCancelled || generation != loadGeneration { return }
                    guard let image else {
                        unreadable.append(path)
                        continue
                    }
                    land(image, for: path, data: data)
                    let side = loaded.background(dark: active) == .image(path: path) ? "active" : "inactive"
                    Self.signposter.emitEvent("Wallpaper applied", id: signpost, "\(side, privacy: .public)")
                }
                problem = unreadable.isEmpty ? nil : "The background image \(unreadable.joined(separator: ", ")) can't be read."
            } catch {
                if Task.isCancelled || error is CancellationError || (error as? URLError)?.code == .cancelled { return }
                problem = AgenthailAPIError.userMessage(error)
            }
        }
    }

    static func prefersDark(_ config: UIConfig) -> Bool {
        switch config.theme {
        case "dark": return true
        case "light": return false
        default: return UITraitCollection.current.userInterfaceStyle == .dark
        }
    }

    static func fetchOrder(_ config: UIConfig, dark: Bool) -> [String] {
        var paths: [String] = []
        for case .image(let path) in [config.background(dark: dark), config.background(dark: !dark)] where !paths.contains(path) { paths.append(path) }
        return paths
    }

    private func adopt(_ loaded: UIConfig) {
        let active = Self.prefersDark(loaded)
        var next: [String: WallpaperImage] = [:]
        for dark in [active, !active] {
            guard case .image(let path) = loaded.background(dark: dark), next[path] == nil else { continue }
            if let real = wallpapers.values.first(where: { $0.id == path }) {
                next[path] = real
            } else if case .image(let shown) = config.background(dark: dark), let image = wallpapers[shown] {
                next[path] = image
            }
        }
        guard loaded != config || next != wallpapers else { return }
        config = loaded
        wallpapers = next
        forgetRenders()
        save(loaded, wallpapers: [:])
    }

    private func land(_ image: WallpaperImage, for path: String, data: Data) {
        guard config.imagePaths.contains(path) else { return }
        wallpapers[path] = image
        forgetRenders()
        save(config, wallpapers: [path: data])
    }

    func preview(_ config: UIConfig, wallpapers: [WallpaperImage] = []) {
        self.config = config
        self.wallpapers = Dictionary(wallpapers.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        forgetRenders()
    }

    private func forgetRenders() {
        rendered = [:]
        layered = [:]
        extremes = [:]
        recent = []
        pending = []
    }

    private func save(_ config: UIConfig, wallpapers fetched: [String: Data]) {
        guard let cache else { return }
        let files = FileManager.default
        try? files.createDirectory(at: cache, withIntermediateDirectories: true)
        let shown = Dictionary(config.imagePaths.compactMap { path in wallpapers[path].map { (path, $0.id) } }, uniquingKeysWith: { first, _ in first })
        let kept = Set((config.imagePaths + shown.values).map(Self.wallpaperFile))
        for file in (try? files.contentsOfDirectory(at: cache, includingPropertiesForKeys: nil)) ?? [] {
            let name = file.lastPathComponent
            if file.pathExtension == "png" || name.hasSuffix(".facts.json") || (name.hasPrefix("wallpaper") && !kept.contains(name)) {
                try? files.removeItem(at: file)
            }
        }
        for (path, data) in fetched {
            try? data.write(to: cache.appendingPathComponent(Self.wallpaperFile(path)), options: .atomic)
        }
        try? JSONEncoder().encode(config).write(to: cache.appendingPathComponent("ui.json"), options: .atomic)
        try? JSONEncoder().encode(shown.filter { $0.key != $0.value }).write(to: cache.appendingPathComponent("shown.json"), options: .atomic)
    }

    nonisolated private static func wallpaperFile(_ path: String) -> String {
        "wallpaper-" + SHA256.hash(data: Data(path.utf8)).map { String(format: "%02x", $0) }.joined()
    }

    private func wallpaperID(dark: Bool) -> String {
        if case .image(let path) = config.background(dark: dark) { return wallpapers[path]?.id ?? "missing:" + path }
        return ""
    }

    private func renderScale(_ key: Key) -> CGFloat {
        key.surface == .list ? key.scale : 1
    }

    func file(_ key: Key) -> URL? {
        let name = [RenderStamp.current, config.rev, wallpaperID(dark: key.dark), "\(key.surface)", "\(Int(key.size.width))x\(Int(key.size.height))@\(renderScale(key))",
                    key.dark ? "dark" : "light", key.solid ? "solid" : "glass", "\(key.target)", key.readable ? "readable" : "plain"].joined(separator: "-")
        let safe = name.map { $0.isLetter || $0.isNumber || $0 == "." || $0 == "-" || $0 == "@" ? $0 : "_" }
        return cache?.appendingPathComponent(String(safe) + ".png")
    }

    func request(_ key: Key) {
        guard active, rendered[key] == nil, layered[key] == nil, !pending.contains(key), key.size.width > 0, key.size.height > 0 else { return }
        if key.surface == .transcript {
            requestLayers(key)
            return
        }
        if let file = file(key), let saved = PhoneRenderCache.read(file, size: key.size) {
            keep(saved, for: key)
            return
        }
        pending.insert(key)
        let target = file(key)
        let shown = spec(key)
        let config = config
        let wallpaper = wallpaperID(dark: key.dark)
        Task {
            let result = await Task.detached(priority: .userInitiated) { BackdropRenderer.render(shown) }.value
            guard config == self.config, wallpaper == self.wallpaperID(dark: key.dark) else { return }
            pending.remove(key)
            guard let result else { return }
            keep(result, for: key)
            if let target { Task.detached(priority: .utility) { PhoneRenderCache.write(result, to: target) } }
        }
    }

    private func requestLayers(_ key: Key) {
        if let file = file(key), let saved = PhoneRenderCache.readLayers(file, polarity: key.dark ? .dark : .light), saved.size == key.size {
            keep(saved, for: key)
            return
        }
        pending.insert(key)
        let target = file(key)
        let shown = spec(key)
        let style = BackdropSpec.PaneStyle(minimum: key.solid ? 1 : Float(config.paneOpacity), strength: shown.strength)
        let config = config
        let wallpaper = wallpaperID(dark: key.dark)
        Task {
            let result = await Task.detached(priority: .userInitiated) { BackdropRenderer.layers(shown, panes: [.content: style]) }.value
            guard config == self.config, wallpaper == self.wallpaperID(dark: key.dark) else { return }
            pending.remove(key)
            guard let result else { return }
            keep(result, for: key)
            if let target { Task.detached(priority: .utility) { PhoneRenderCache.write(result, to: target) } }
        }
    }

    private func keep(_ image: BackdropImage, for key: Key) {
        rendered[key] = image
        remember(key)
        let polarity: ScrimField.Polarity = key.dark ? .dark : .light
        measure(key, current: { $0.rendered[key]?.image === image.image }) { GlassSolve.extreme(image, polarity: polarity) }
    }

    private func keep(_ layers: BackdropLayers, for key: Key) {
        layered[key] = layers
        remember(key)
        guard let pane = layers.panes[.content] else { return }
        let polarity: ScrimField.Polarity = key.dark ? .dark : .light
        measure(key, current: { $0.layered[key]?.panes[.content]?.base === pane.base }) {
            GlassSolve.extreme(pane, tint: layers.paneTint, size: layers.size, polarity: polarity)
        }
    }

    private func remember(_ key: Key) {
        recent.removeAll { $0 == key }
        recent.append(key)
        while recent.count > Self.keptRenders {
            let dropped = recent.removeFirst()
            rendered.removeValue(forKey: dropped)
            layered.removeValue(forKey: dropped)
            extremes.removeValue(forKey: dropped)
        }
    }

    private func measure(_ key: Key, current: @escaping (PhoneAppearance) -> Bool, _ extreme: @escaping @Sendable () -> Float?) {
        Task {
            let value = await Task.detached(priority: .utility) { extreme() }.value
            guard current(self) else { return }
            extremes[key] = value
        }
    }

    static let increasedTarget: Float = 7

    static func contrastTarget(_ config: UIConfig, increased: Bool) -> Float {
        increased ? increasedTarget : Float(config.contrastTarget)
    }

    func spec(_ key: Key) -> BackdropSpec {
        let dark = key.dark
        let ground = PhonePalette.ground(dark: dark)
        let roles = PhonePalette.textRoles(TextColors(config), dark: dark, increased: key.target >= Self.increasedTarget)
        let paneTint: BackdropRGB
        switch config.glassTint {
        case "ground": paneTint = ground
        case "accent": paneTint = config.accent.map { BackdropRGB(Float($0.red), Float($0.green), Float($0.blue)) } ?? ground
        default: paneTint = UIConfig.RGBColor(hex: config.glassTint).map { BackdropRGB(Float($0.red), Float($0.green), Float($0.blue)) } ?? ground
        }
        let scrimTint: ScrimField.Tint
        switch config.scrimTint {
        case "auto": scrimTint = .auto
        case "black": scrimTint = .color(0, 0, 0)
        default: scrimTint = UIConfig.RGBColor(hex: config.scrimTint).map { .color(Float($0.red), Float($0.green), Float($0.blue)) } ?? .auto
        }
        let full = CGRect(origin: .zero, size: key.size)
        let strength = Float(key.surface == .pane || key.surface == .transcript ? config.backgroundSessionOpacity : config.backgroundOpacity)
        var panes: [BackdropSpec.Pane] = []
        var texts: [CGRect] = []
        switch key.surface {
        case .pane, .transcript:
            panes = [BackdropSpec.Pane(id: "session", rect: full, radius: 0, minimum: Float(config.paneOpacity), strength: strength)]
        case .cards:
            panes = [BackdropSpec.Pane(id: "cards", rect: full, radius: 0, minimum: Float(config.paneOpacity), strength: strength)]
        case .list:
            texts = [CGRect(x: 0, y: 0, width: key.size.width, height: min(160, key.size.height))]
        }
        return BackdropSpec(
            wallpaper: wallpaperSpec(dark: dark) ?? .linear(angle: 0, stops: []),
            fit: config.backgroundFit,
            position: config.backgroundPosition,
            dim: Float(config.backgroundDim),
            strength: strength,
            texture: key.surface == .list ? config.texture : "none",
            textureStrength: Float(config.textureStrength),
            textureSpacing: CGFloat(config.textureSpacing),
            blur: CGFloat(config.glassBlur),
            paneTint: paneTint,
            scrimTint: scrimTint,
            feather: Float(BackdropSpec.feather),
            target: key.target,
            polarity: dark ? .dark : .light,
            ground: ground,
            roles: roles,
            size: key.size,
            scale: renderScale(key),
            panes: panes,
            texts: texts,
            solidPanes: key.solid,
            readability: key.readable
        )
    }

    func solidPaneColor(dark: Bool, target: Float) -> Color {
        guard active else { return Color(uiColor: .systemBackground) }
        let tint = spec(Key(surface: .transcript, size: .zero, scale: 1, dark: dark, solid: true, target: target)).reachablePaneTint
        return Color(.sRGB, red: Double(tint.0), green: Double(tint.1), blue: Double(tint.2))
    }

    private func wallpaperSpec(dark: Bool) -> BackdropSpec.Wallpaper? {
        func rgb(_ c: UIConfig.RGBColor) -> BackdropRGB { BackdropRGB(Float(c.red), Float(c.green), Float(c.blue)) }
        switch config.background(dark: dark) {
        case .none, .desktop: return nil
        case .image(let path): return wallpapers[path].map { .image($0) }
        case .linear(let angle, let stops): return .linear(angle: Float(angle), stops: stops.map { BackdropStop(color: rgb($0.color), location: Float($0.location)) })
        case .mesh(let columns, let rows, let colors): return .mesh(columns: columns, rows: rows, colors: colors.map(rgb))
        }
    }
}

private struct PhoneAppearanceKey: EnvironmentKey {
    static let defaultValue: PhoneAppearance? = nil
}

/// The list's card layer: the backdrop rendered as one pane, and where it
/// sits on screen, so each card shows the slice under it.
struct CardBackdrop: Equatable {
    let image: BackdropImage?
    let frame: CGRect

    static func == (lhs: CardBackdrop, rhs: CardBackdrop) -> Bool {
        lhs.image?.image === rhs.image?.image && lhs.frame == rhs.frame
    }
}

private struct CardBackdropKey: EnvironmentKey {
    static let defaultValue: CardBackdrop? = nil
}

extension EnvironmentValues {
    var phoneAppearance: PhoneAppearance? {
        get { self[PhoneAppearanceKey.self] }
        set { self[PhoneAppearanceKey.self] = newValue }
    }

    var cardBackdrop: CardBackdrop? {
        get { self[CardBackdropKey.self] }
        set { self[CardBackdropKey.self] = newValue }
    }
}

extension View {
    func phoneBackdrop(_ surface: PhoneAppearance.Surface) -> some View {
        modifier(PhoneBackdropModifier(surface: surface))
    }

    func floatingGlass<S: Shape>(in shape: S, interactive: Bool = false, lifted: Bool = true) -> some View {
        modifier(FloatingGlass(shape: shape, interactive: interactive, lifted: lifted))
    }
}

private struct PhoneBackdropModifier: ViewModifier {
    let surface: PhoneAppearance.Surface
    @Environment(\.phoneAppearance) private var appearance

    func body(content: Content) -> some View {
        if let appearance {
            PhoneBackdropBody(content: content, surface: surface, appearance: appearance)
        } else {
            content.background(Color(uiColor: .systemBackground).ignoresSafeArea())
        }
    }
}

private struct PhoneBackdropBody<Content: View>: View {
    let content: Content
    let surface: PhoneAppearance.Surface
    @ObservedObject var appearance: PhoneAppearance
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.displayScale) private var displayScale
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency
    @Environment(\.colorSchemeContrast) private var contrast
    @State private var frame: CGRect = .zero
    @State private var scrim = TranscriptScrim()

    private func key(_ surface: PhoneAppearance.Surface) -> PhoneAppearance.Key {
        PhoneAppearance.Key(surface: surface, size: frame.size, scale: displayScale, dark: colorScheme == .dark,
                            solid: appearance.config.solidSurfaces(dark: colorScheme == .dark, reduceTransparency: reduceTransparency),
                            target: PhoneAppearance.contrastTarget(appearance.config, increased: contrast == .increased),
                            readable: appearance.config.readability || contrast == .increased)
    }

    private var keys: [PhoneAppearance.Key] {
        surface == .list ? [key(.list), key(.cards)] : [key(surface)]
    }

    private func request() {
        for key in keys { appearance.request(key) }
    }

    var body: some View {
        let rendered = appearance.active ? appearance.rendered[key(surface)] : nil
        let layered = appearance.active && surface == .transcript ? appearance.layered[key(surface)] : nil
        let cards = appearance.active && surface == .list ? CardBackdrop(image: appearance.rendered[key(.cards)], frame: frame) : nil
        let shown = key(surface)
        let ground = surface == .transcript && shown.solid ? appearance.solidPaneColor(dark: shown.dark, target: shown.target)
            : Color(uiColor: surface == .list && !appearance.active ? .systemGroupedBackground : .systemBackground)
        content
            .environment(\.cardBackdrop, cards)
            .environment(\.backdropKey, key(surface))
            .environment(\.transcriptScrim, surface == .transcript ? scrim : nil)
            .scrollContentBackground(appearance.active ? .hidden : .automatic)
            .background {
                BackdropLayer(image: rendered?.image, ground: ground, frame: $frame) {
                    if let layered { TranscriptBackdropLayer(layers: layered, scrim: scrim).transition(.opacity) }
                }
            }
            .animation(.easeOut(duration: 0.25), value: rendered == nil && layered == nil)
            .task(id: keys) { request() }
            .onChange(of: appearance.config) { request() }
            .onChange(of: appearance.wallpapers) { request() }
    }
}

private struct BackdropLayer<Layered: View>: View {
    let image: CGImage?
    let ground: Color
    @Binding var frame: CGRect
    @ViewBuilder let layered: Layered

    var body: some View {
        GeometryReader { proxy in
            let size = proxy.size
            ZStack {
                ground
                if let image {
                    Image(decorative: image, scale: CGFloat(image.width) / max(size.width, 1))
                        .resizable()
                        .transition(.opacity)
                }
                layered
            }
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { frame = $0 }
        }
        .ignoresSafeArea()
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

struct AppearanceScope<Content: View>: View {
    @ObservedObject var appearance: PhoneAppearance
    @ViewBuilder let content: Content

    var body: some View {
        content
            .environment(\.phoneAppearance, appearance)
            .environment(\.textColors, TextColors(appearance.config))
            .tint(PhonePalette.accentText)
            .preferredColorScheme(appearance.colorScheme)
            .modifier(PhoneTextScale(steps: PhoneTextScale.steps(appearance.textScale)))
    }
}

extension EnvironmentValues {
    @Entry var readingTypeSize: DynamicTypeSize = .large
    @Entry var chromeTypeSize: DynamicTypeSize = .large
}

/// Controls and status grow at most one Dynamic Type step; the transcript takes the whole Text size through readingTypeSize.
struct PhoneTextScale: ViewModifier {
    static let chromeSteps = 1
    let steps: Int
    @Environment(\.dynamicTypeSize) private var system

    static let unchanged = DynamicTypeSize.xSmall ... DynamicTypeSize.accessibility5

    func body(content: Content) -> some View {
        let chrome = Self.shifted(system, by: min(steps, Self.chromeSteps))
        return content
            .dynamicTypeSize(chrome == system ? Self.unchanged : chrome ... chrome)
            .environment(\.readingTypeSize, Self.shifted(system, by: steps))
            .environment(\.chromeTypeSize, chrome)
    }

    static func steps(_ scale: Double) -> Int {
        Int(((scale - 1) / 0.1).rounded())
    }

    static func shifted(_ size: DynamicTypeSize, by steps: Int) -> DynamicTypeSize {
        let all = DynamicTypeSize.allCases
        guard let index = all.firstIndex(of: size) else { return size }
        return all[min(max(index + steps, 0), all.count - 1)]
    }
}

private struct ReadingTypeSize: ViewModifier {
    @Environment(\.readingTypeSize) private var reading
    @Environment(\.chromeTypeSize) private var chrome
    @Environment(\.dynamicTypeSize) private var current
    let forReading: Bool

    func body(content: Content) -> some View {
        let size = forReading ? reading : chrome
        return content.dynamicTypeSize(size == current ? PhoneTextScale.unchanged : size ... size)
    }
}

extension View {
    func readingTypeSize() -> some View {
        modifier(ReadingTypeSize(forReading: true))
    }

    /// Sheets opened from the transcript would otherwise inherit its reading size.
    func chromeTypeSize() -> some View {
        modifier(ReadingTypeSize(forReading: false))
    }
}

struct PhoneTextSizeSection: View {
    var model: AgenthailIOSModel
    @ObservedObject private var appearance: PhoneAppearance

    init(model: AgenthailIOSModel) {
        self.model = model
        appearance = model.appearance
    }

    var body: some View {
        if let problem = appearance.problem {
            Section("Appearance") {
                Label(problem, systemImage: "exclamationmark.triangle")
                    .accessibilityIdentifier("appearance-problem")
            }
        }
        Section {
            Stepper(value: Binding(get: { appearance.textScale }, set: { model.setTextScale($0) }), in: 0.8...1.6, step: 0.1) {
                LabeledContent("Text size", value: "\(Int((appearance.textScale * 100).rounded()))%")
            }
            .accessibilityIdentifier("text-size")
        } header: {
            Text("Text")
        } footer: {
            Text(appearance.textScaleProblem ?? "Adds to the text size in iOS Settings, for this iPhone only.")
        }
    }
}
