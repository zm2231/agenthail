import ImageIO
import SwiftUI
import UIKit
import UniformTypeIdentifiers

enum PhonePalette {
    private static let groundHex = PaletteTokens.window
    private static let textHex = PaletteTokens.text
    private static let text2Hex = PaletteTokens.text2
    private static let mutedHex = PaletteTokens.muted
    private static let attentionHex = PaletteTokens.attention
    private static let workingHex = PaletteTokens.working
    private static let doneHex = PaletteTokens.done
    private static let failedHex = PaletteTokens.failed

    static let text = dynamic(textHex)
    static let text2 = dynamic(text2Hex)
    static let muted = dynamic(mutedHex)
    static let attention = dynamic(attentionHex)
    static let working = dynamic(workingHex)
    static let done = dynamic(doneHex)
    static let failed = dynamic(failedHex)
    static let ground = dynamic(groundHex)
    static let hairline = Color(uiColor: UIColor { $0.userInterfaceStyle == .dark ? UIColor(white: 1, alpha: 0.10) : UIColor(white: 0, alpha: 0.08) })

    static func textRoles(dark: Bool) -> [BackdropRGB] {
        PaletteTokens.textRoles.map { rgb(dark ? $0.dark : $0.light) }
    }

    static func ground(dark: Bool) -> BackdropRGB { rgb(dark ? groundHex.dark : groundHex.light) }

    static func status(_ session: SessionState) -> Color? {
        if session.isWaitingOnYou { return attention }
        if session.isUnavailable { return failed }
        if session.isWorking || session.isWaitingOnBackground { return working }
        return nil
    }

    private static func rgb(_ hex: UInt32) -> BackdropRGB {
        BackdropRGB(Float((hex >> 16) & 0xFF) / 255, Float((hex >> 8) & 0xFF) / 255, Float(hex & 0xFF) / 255)
    }

    private static func dynamic(_ pair: (light: UInt32, dark: UInt32)) -> Color {
        Color(uiColor: UIColor { traits in
            let hex = traits.userInterfaceStyle == .dark ? pair.dark : pair.light
            return UIColor(red: CGFloat((hex >> 16) & 0xFF) / 255, green: CGFloat((hex >> 8) & 0xFF) / 255, blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
        })
    }
}

@MainActor
final class PhoneAppearance: ObservableObject {
    /// A list shows the background as it is and its cards show the same
    /// background as a pane; a session, Settings and the Inbox read on one
    /// pane that covers the screen.
    enum Surface: Hashable {
        case list
        case cards
        case pane
    }

    struct Key: Hashable {
        var surface: Surface
        var size: CGSize
        var scale: CGFloat
        var dark: Bool
        var solid: Bool
        var target: Float

        func hash(into hasher: inout Hasher) {
            hasher.combine(surface)
            hasher.combine(size.width)
            hasher.combine(size.height)
            hasher.combine(scale)
            hasher.combine(dark)
            hasher.combine(solid)
            hasher.combine(target)
        }
    }

    @Published private(set) var config = UIConfig.defaults
    @Published private(set) var wallpaper: WallpaperImage?
    @Published private(set) var rendered: [Key: BackdropImage] = [:]
    @Published private(set) var problem: String?
    private var pending: Set<Key> = []
    private var recent: [Key] = []
    private static let keptRenders = 6
    private var loadTask: Task<Void, Never>?
    private let cache: URL?

    init(cache: URL? = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first?.appendingPathComponent("appearance", isDirectory: true)) {
        self.cache = cache
        if let cache, let data = try? Data(contentsOf: cache.appendingPathComponent("ui.json")),
           let saved = try? JSONDecoder().decode(UIConfig.self, from: data) {
            config = saved
            if case .image(let path) = saved.background, let image = try? Data(contentsOf: cache.appendingPathComponent("wallpaper")) {
                wallpaper = WallpaperImage.decode(image, id: path, maxPixels: Self.maxPixels)
            }
        }
    }

    private static var maxPixels: Int {
        let screen = UIScreen.main.nativeBounds.size
        return Int(max(screen.width, screen.height))
    }

    static var deviceKind: String { UIDevice.current.userInterfaceIdiom == .pad ? "tablet" : "phone" }

    var active: Bool {
        guard config.glass else { return false }
        switch config.background {
        case .none, .desktop: return false
        case .image(let path): return wallpaper?.id == path
        case .linear, .mesh: return true
        }
    }

    var colorScheme: ColorScheme? {
        switch config.theme {
        case "dark": return .dark
        case "light": return .light
        default: return nil
        }
    }

    var accent: Color {
        guard let accent = config.accent else { return SessionStyle.accent }
        return Color(red: accent.red, green: accent.green, blue: accent.blue)
    }

    func load(from api: AgenthailAPI) {
        loadTask?.cancel()
        loadTask = Task {
            do {
                let loaded = try await api.uiConfig(device: UIDevice.current.name, kind: Self.deviceKind)
                if Task.isCancelled { return }
                var wallpaper = wallpaperID(loaded) == nil ? nil : self.wallpaper
                var wallpaperData: Data?
                if case .image(let path) = loaded.background, wallpaper?.id != path {
                    let data = try await api.uiBackground(path: path)
                    let maxPixels = Self.maxPixels
                    wallpaper = await Task.detached { WallpaperImage.decode(data, id: path, maxPixels: maxPixels) }.value
                    if Task.isCancelled { return }
                    guard wallpaper != nil else {
                        self.problem = "The background image \(path) can't be read."
                        return
                    }
                    wallpaperData = data
                }
                if loaded != config || wallpaper != self.wallpaper {
                    config = loaded
                    self.wallpaper = wallpaper
                    rendered = [:]
                    recent = []
                    pending = []
                    save(loaded, wallpaper: wallpaperData)
                }
                problem = nil
            } catch {
                if Task.isCancelled || error is CancellationError || (error as? URLError)?.code == .cancelled { return }
                problem = AgenthailAPIError.userMessage(error)
            }
        }
    }

    func preview(_ config: UIConfig, wallpaper: WallpaperImage? = nil) {
        self.config = config
        self.wallpaper = wallpaper
        rendered = [:]
        recent = []
        pending = []
    }

    private func save(_ config: UIConfig, wallpaper: Data?) {
        guard let cache else { return }
        let files = FileManager.default
        try? files.createDirectory(at: cache, withIntermediateDirectories: true)
        for file in (try? files.contentsOfDirectory(at: cache, includingPropertiesForKeys: nil)) ?? [] where file.pathExtension == "png" {
            try? files.removeItem(at: file)
        }
        let image = cache.appendingPathComponent("wallpaper")
        if let wallpaper {
            try? wallpaper.write(to: image, options: .atomic)
        } else if wallpaperID(config) == nil {
            try? files.removeItem(at: image)
        }
        try? JSONEncoder().encode(config).write(to: cache.appendingPathComponent("ui.json"), options: .atomic)
    }

    private func wallpaperID(_ config: UIConfig) -> String? {
        if case .image(let path) = config.background { return path }
        return nil
    }

    private func renderScale(_ key: Key) -> CGFloat {
        key.surface == .list ? key.scale : 1
    }

    func file(_ key: Key) -> URL? {
        let name = [config.rev, wallpaper?.id ?? "", "\(key.surface)", "\(Int(key.size.width))x\(Int(key.size.height))@\(renderScale(key))",
                    key.dark ? "dark" : "light", key.solid ? "solid" : "glass", "\(key.target)"].joined(separator: "-")
        let safe = name.map { $0.isLetter || $0.isNumber || $0 == "." || $0 == "-" || $0 == "@" ? $0 : "_" }
        return cache?.appendingPathComponent(String(safe) + ".png")
    }

    func request(_ key: Key) {
        guard active, rendered[key] == nil, !pending.contains(key), key.size.width > 0, key.size.height > 0 else { return }
        if let file = file(key), let saved = Self.read(file, size: key.size) {
            keep(saved, for: key)
            return
        }
        pending.insert(key)
        let target = file(key)
        let shown = spec(key)
        let config = config
        Task {
            let result = await Task.detached(priority: .userInitiated) { BackdropRenderer.render(shown) }.value
            guard config == self.config else { return }
            pending.remove(key)
            guard let result else { return }
            keep(result, for: key)
            if let target { Task.detached(priority: .utility) { Self.write(result, to: target) } }
        }
    }

    private func keep(_ image: BackdropImage, for key: Key) {
        rendered[key] = image
        recent.removeAll { $0 == key }
        recent.append(key)
        while recent.count > Self.keptRenders { rendered.removeValue(forKey: recent.removeFirst()) }
    }

    nonisolated private static func write(_ rendered: BackdropImage, to url: URL) {
        guard let destination = CGImageDestinationCreateWithURL(url as CFURL, UTType.png.identifier as CFString, 1, nil) else { return }
        CGImageDestinationAddImage(destination, rendered.image, nil)
        CGImageDestinationFinalize(destination)
    }

    private static func read(_ url: URL, size: CGSize) -> BackdropImage? {
        guard let source = CGImageSourceCreateWithURL(url as CFURL, nil),
              let image = CGImageSourceCreateImageAtIndex(source, 0, nil) else { return nil }
        return BackdropImage(image: image, size: size, paneMaxima: [:], paneTint: BackdropRGB(0, 0, 0))
    }

    static func contrastTarget(_ config: UIConfig, increased: Bool) -> Float {
        increased ? 7 : Float(config.contrastTarget)
    }

    func spec(_ key: Key) -> BackdropSpec {
        let dark = key.dark
        let ground = PhonePalette.ground(dark: dark)
        let roles = PhonePalette.textRoles(dark: dark)
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
        let strength = Float(key.surface == .pane ? config.backgroundSessionOpacity : config.backgroundOpacity)
        var panes: [BackdropSpec.Pane] = []
        var texts: [CGRect] = []
        switch key.surface {
        case .pane:
            panes = [BackdropSpec.Pane(id: "session", rect: full, radius: 0, minimum: Float(config.contentOpacity), strength: strength)]
        case .cards:
            panes = [BackdropSpec.Pane(id: "cards", rect: full, radius: 0, minimum: Float(config.sidebarOpacity), strength: strength)]
        case .list:
            texts = [CGRect(x: 0, y: 0, width: key.size.width, height: min(160, key.size.height))]
        }
        return BackdropSpec(
            wallpaper: wallpaperSpec ?? .linear(angle: 0, stops: []),
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
            feather: Float(config.scrimFeather),
            target: key.target,
            polarity: dark ? .dark : .light,
            ground: ground,
            roles: roles,
            size: key.size,
            scale: renderScale(key),
            panes: panes,
            texts: texts,
            solidPanes: key.solid
        )
    }

    private var wallpaperSpec: BackdropSpec.Wallpaper? {
        func rgb(_ c: UIConfig.RGBColor) -> BackdropRGB { BackdropRGB(Float(c.red), Float(c.green), Float(c.blue)) }
        switch config.background {
        case .none, .desktop: return nil
        case .image(let path): return wallpaper?.id == path ? .image(wallpaper!) : nil
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

    func floatingGlass<S: Shape>(in shape: S, interactive: Bool = false) -> some View {
        modifier(FloatingGlass(shape: shape, interactive: interactive))
    }

    /// Floating controls over the edge of a scroll view; content fades out
    /// under them instead of showing through behind their text.
    func edgeBar<Bar: View>(_ edge: VerticalEdge, spacing: CGFloat? = nil, @ViewBuilder content: () -> Bar) -> some View {
        modifier(EdgeBar(edge: edge, spacing: spacing, bar: content()))
    }
}

private struct EdgeBar<Bar: View>: ViewModifier {
    let edge: VerticalEdge
    let spacing: CGFloat?
    let bar: Bar

    func body(content: Content) -> some View {
        if #available(iOS 26, *) {
            content
                .safeAreaBar(edge: edge, spacing: spacing) { bar }
                .scrollEdgeEffectStyle(.soft, for: edge == .top ? .top : .bottom)
        } else {
            content.safeAreaInset(edge: edge, spacing: spacing) {
                bar.background {
                    Rectangle().fill(.ultraThinMaterial)
                        .mask(LinearGradient(colors: [.black, .black.opacity(0)], startPoint: edge == .top ? .top : .bottom, endPoint: edge == .top ? .bottom : .top))
                        .ignoresSafeArea(edges: edge == .top ? .top : .bottom)
                }
            }
        }
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

    private func key(_ surface: PhoneAppearance.Surface) -> PhoneAppearance.Key {
        PhoneAppearance.Key(surface: surface, size: frame.size, scale: displayScale, dark: colorScheme == .dark, solid: reduceTransparency,
                            target: PhoneAppearance.contrastTarget(appearance.config, increased: contrast == .increased))
    }

    private var keys: [PhoneAppearance.Key] {
        surface == .list ? [key(.list), key(.cards)] : [key(surface)]
    }

    private func request() {
        for key in keys { appearance.request(key) }
    }

    var body: some View {
        let rendered = appearance.active ? appearance.rendered[key(surface)] : nil
        let cards = appearance.active && surface == .list ? CardBackdrop(image: appearance.rendered[key(.cards)], frame: frame) : nil
        content
            .environment(\.cardBackdrop, cards)
            .scrollContentBackground(appearance.active ? .hidden : .automatic)
            .background {
                BackdropLayer(image: rendered?.image, grouped: surface == .list && !appearance.active, frame: $frame)
            }
            .animation(.easeOut(duration: 0.25), value: rendered == nil)
            .task(id: keys) { request() }
            .onChange(of: appearance.config) { request() }
            .onChange(of: appearance.wallpaper) { request() }
    }
}

private struct BackdropLayer: View {
    let image: CGImage?
    let grouped: Bool
    @Binding var frame: CGRect

    var body: some View {
        GeometryReader { proxy in
            let size = proxy.size
            ZStack {
                Color(uiColor: grouped ? .systemGroupedBackground : .systemBackground)
                if let image {
                    Image(decorative: image, scale: CGFloat(image.width) / max(size.width, 1))
                        .resizable()
                        .transition(.opacity)
                }
            }
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { frame = $0 }
        }
        .ignoresSafeArea()
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

private struct FloatingGlass<S: Shape>: ViewModifier {
    let shape: S
    let interactive: Bool
    @Environment(\.phoneAppearance) private var appearance
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    func body(content: Content) -> some View {
        let tint = PhonePalette.ground.opacity(appearance?.config.composerOpacity ?? 0.5)
        if reduceTransparency {
            content.background(Color(uiColor: .secondarySystemBackground), in: shape)
        } else if #available(iOS 26, *) {
            content.glassEffect(interactive ? .regular.tint(tint).interactive() : .regular.tint(tint), in: shape)
        } else {
            content.background(tint, in: shape).background(.regularMaterial, in: shape)
        }
    }
}

struct AppearanceScope<Content: View>: View {
    @ObservedObject var appearance: PhoneAppearance
    @ViewBuilder let content: Content

    var body: some View {
        content
            .environment(\.phoneAppearance, appearance)
            .tint(appearance.accent)
            .preferredColorScheme(appearance.colorScheme)
    }
}
