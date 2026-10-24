import AppKit
import SwiftUI

@MainActor
final class WindowBackdrop: ObservableObject {
    enum PaneRole: Equatable {
        case sidebar
        case content
        case chrome

        var layer: BackdropRole? {
            switch self {
            case .sidebar: .sidebar
            case .content: .content
            case .chrome: nil
            }
        }
    }

    @Published private(set) var layers: BackdropLayerSet?
    @Published private(set) var active = false
    @Published private(set) var desktop = false
    @Published private(set) var gutter: CGFloat = 0
    @Published private(set) var border: Double = 0
    @Published private(set) var composerOpacity: Double = 0.5
    @Published private(set) var feather: CGFloat = 240
    @Published private(set) var tints: [PaneRole: CGFloat] = [:]
    @Published private(set) var solid = false

    private var windowSize: CGSize = .zero
    private var scale: CGFloat = 2
    private var dark = true
    private var config = UIConfig.defaults
    private var wallpapers: [String: WallpaperImage] = [:]
    private var renderTask: Task<Void, Never>?
    private(set) var rendering = false
    private var pending = false
    private(set) var generation = 0
    private var rendered: BackdropSpec?
    private weak var window: NSWindow?
    nonisolated(unsafe) private var observers: [(NotificationCenter, NSObjectProtocol)] = []

    init() {
        let watched = [
            (NSWorkspace.shared.notificationCenter, NSWorkspace.accessibilityDisplayOptionsDidChangeNotification),
            (NotificationCenter.default, NSColor.systemColorsDidChangeNotification),
        ]
        observers = watched.map { center, name in
            (center, center.addObserver(forName: name, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.refresh() }
            })
        }
    }

    deinit {
        for (center, observer) in observers { center.removeObserver(observer) }
    }

    var paneRadius: CGFloat { gutter > 0 ? DesktopRadius.sheet : 0 }

    func configure(_ config: UIConfig, wallpapers: [String: WallpaperImage]) {
        self.config = config
        self.wallpapers = wallpapers
        refresh()
    }

    private func refresh() {
        generation += 1
        let wanted = Self.wallpaper(config.background(dark: dark), images: wallpapers) != nil
        if active != wanted { active = wanted }
        let desktop = config.background(dark: dark) == .desktop
        if self.desktop != desktop { self.desktop = desktop }
        let gutter = wanted ? CGFloat(config.gutter) : 0
        if self.gutter != gutter { self.gutter = gutter }
        if border != config.border { border = config.border }
        let workspace = NSWorkspace.shared
        let solid = config.solidSurfaces(dark: dark, reduceTransparency: workspace.accessibilityDisplayShouldReduceTransparency)
        if self.solid != solid { self.solid = solid }
        let floating = solid ? 1 : DesktopGlass.floatingOpacity(config, dark: dark, increaseContrast: workspace.accessibilityDisplayShouldIncreaseContrast)
        if composerOpacity != floating { composerOpacity = floating }
        let feather = CGFloat(config.scrimFeather)
        if self.feather != feather { self.feather = feather }
        let tints: [PaneRole: CGFloat] = [
            .sidebar: solid ? 1 : CGFloat(config.sidebarOpacity),
            .content: solid ? 1 : CGFloat(config.contentOpacity),
            .chrome: solid ? 1 : CGFloat(config.chromeOpacity),
        ]
        if self.tints != tints { self.tints = tints }
        if !wanted || layers.map({ $0.layers.polarity != polarity }) == true { layers = nil; rendered = nil }
        schedule()
    }

    private var polarity: ScrimField.Polarity { dark ? .dark : .light }

    func windowChanged(_ window: NSWindow) {
        self.window = window
        let size = window.contentView?.bounds.size ?? .zero
        if windowSize != size { windowSize = size }
        scale = window.backingScaleFactor
        let dark = DesktopPalette.isDark(window.effectiveAppearance)
        if self.dark != dark {
            self.dark = dark
            refresh()
        }
        if desktop {
            window.isOpaque = false
            window.backgroundColor = .clear
        } else if !window.isOpaque {
            window.isOpaque = true
            window.backgroundColor = .windowBackgroundColor
        }
        schedule()
    }

    private func schedule() {
        guard active else { return }
        if rendering {
            pending = true
            return
        }
        renderTask?.cancel()
        renderTask = Task { [weak self] in
            let live = self?.window?.inLiveResize == true
            try? await Task.sleep(for: .milliseconds(live ? 16 : 120))
            guard !Task.isCancelled, let self else { return }
            await self.render(live: live)
        }
    }

    private func render(live: Bool) async {
        guard var spec = spec() else { return }
        if live { spec.scale = min(spec.scale, 1) }
        guard spec != rendered else { return }
        rendering = true
        let started = generation
        let styles = paneStyles()
        let layers = await Task.detached(priority: .userInitiated) { BackdropRenderer.layers(spec, panes: styles) }.value
        rendering = false
        if let layers, started == generation, active {
            rendered = spec
            self.layers = BackdropLayerSet(layers)
        }
        if pending {
            pending = false
            schedule()
        }
    }

    var current: Bool { !active || (rendered != nil && spec() == rendered) }

    private func paneStyles() -> [BackdropRole: BackdropSpec.PaneStyle] {
        [
            .sidebar: BackdropSpec.PaneStyle(minimum: Float(tints[.sidebar] ?? 0), strength: Float(config.backgroundOpacity)),
            .content: BackdropSpec.PaneStyle(minimum: Float(tints[.content] ?? 0), strength: Float(config.backgroundSessionOpacity)),
        ]
    }

    private func spec() -> BackdropSpec? {
        guard windowSize.width > 0, windowSize.height > 0, let wallpaper = Self.wallpaper(config.background(dark: dark), images: wallpapers) else { return nil }
        let appearance = NSAppearance(named: dark ? .darkAqua : .aqua)!
        let ground = DesktopPalette.ground(dark: dark)
        let colors = TextColors(config)
        let roles = DesktopGlass.roles(config, dark: dark, increased: NSWorkspace.shared.accessibilityDisplayShouldIncreaseContrast)
        let paneTint: AccentContrast.RGB
        switch config.glassTint {
        case "ground": paneTint = ground
        case "accent": paneTint = DesktopPalette.accentRGB(colors, in: appearance)
        default: paneTint = UIConfig.RGBColor(hex: config.glassTint).map { AccentContrast.RGB(r: $0.red, g: $0.green, b: $0.blue) } ?? ground
        }
        let scrimTint: ScrimField.Tint
        switch config.scrimTint {
        case "auto": scrimTint = .auto
        case "black": scrimTint = .color(0, 0, 0)
        default:
            let color = UIConfig.RGBColor(hex: config.scrimTint)
            scrimTint = color.map { .color(Float($0.red), Float($0.green), Float($0.blue)) } ?? .auto
        }
        let workspace = NSWorkspace.shared
        return BackdropSpec(
            wallpaper: wallpaper,
            fit: config.backgroundFit,
            position: config.backgroundPosition,
            dim: Float(config.backgroundDim),
            strength: Float(config.backgroundOpacity),
            texture: config.texture,
            textureStrength: Float(config.textureStrength),
            textureSpacing: CGFloat(config.textureSpacing),
            blur: CGFloat(config.glassBlur),
            paneTint: Self.rgb(paneTint),
            scrimTint: scrimTint,
            feather: Float(config.scrimFeather),
            target: workspace.accessibilityDisplayShouldIncreaseContrast ? 7 : Float(config.contrastTarget),
            polarity: dark ? .dark : .light,
            ground: Self.rgb(ground),
            roles: roles.map(Self.rgb),
            size: windowSize,
            scale: scale,
            solidPanes: solid,
            readability: !solid && (config.readability || workspace.accessibilityDisplayShouldIncreaseContrast)
        )
    }

    private static func rgb(_ c: AccentContrast.RGB) -> BackdropRGB {
        BackdropRGB(Float(c.r), Float(c.g), Float(c.b))
    }

    private static func rgb(_ c: UIConfig.RGBColor) -> BackdropRGB {
        BackdropRGB(Float(c.red), Float(c.green), Float(c.blue))
    }

    private static func wallpaper(_ background: UIConfig.Background, images: [String: WallpaperImage]) -> BackdropSpec.Wallpaper? {
        switch background {
        case .none: return nil
        case .desktop: return .desktop
        case .image(let path): return images[path].map { .image($0) }
        case .linear(let angle, let stops): return .linear(angle: Float(angle), stops: stops.map { BackdropStop(color: rgb($0.color), location: Float($0.location)) })
        case .mesh(let columns, let rows, let colors): return .mesh(columns: columns, rows: rows, colors: colors.map(rgb))
        }
    }
}

struct BackdropPlace {
    var role: BackdropRole?
    var anchor: ScrimAnchor?
}

private struct WindowBackdropKey: EnvironmentKey {
    static let defaultValue: WindowBackdrop? = nil
}

private struct BackdropPlaceKey: EnvironmentKey {
    static let defaultValue = BackdropPlace()
}

extension EnvironmentValues {
    var windowBackdrop: WindowBackdrop? {
        get { self[WindowBackdropKey.self] }
        set { self[WindowBackdropKey.self] = newValue }
    }

    var backdropPlace: BackdropPlace {
        get { self[BackdropPlaceKey.self] }
        set { self[BackdropPlaceKey.self] = newValue }
    }
}
