import AppKit
import SwiftUI

@MainActor
final class WindowBackdrop: ObservableObject {
    enum Region: Equatable {
        case pane(PaneRole)
        case text
    }

    enum PaneRole: Equatable {
        case sidebar
        case content
        case chrome
    }

    @Published private(set) var image: BackdropImage?
    @Published private(set) var windowSize: CGSize = .zero
    @Published private(set) var active = false
    @Published private(set) var desktop = false
    @Published private(set) var gutter: CGFloat = 0
    @Published private(set) var border: Double = 0
    @Published private(set) var composerOpacity: Double = 0.5

    /// Keyed by reader, not by region id: two views can briefly report the
    /// same id while one replaces the other, and each withdraws only its own.
    private(set) var regions: [UUID: (id: String, rect: CGRect, region: Region)] = [:]
    private var probes: [UUID: WeakProbe] = [:]
    private var scale: CGFloat = 2
    private var dark = true
    private var config = UIConfig.defaults
    private var wallpaper: WallpaperImage?
    private var renderTask: Task<Void, Never>?
    private var rendering = false
    private var pending = false
    private var rendered: BackdropSpec?
    private weak var window: NSWindow?
    nonisolated(unsafe) private var accessibilityObserver: NSObjectProtocol?

    init() {
        accessibilityObserver = NSWorkspace.shared.notificationCenter.addObserver(forName: NSWorkspace.accessibilityDisplayOptionsDidChangeNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.schedule() }
        }
    }

    deinit {
        if let accessibilityObserver { NSWorkspace.shared.notificationCenter.removeObserver(accessibilityObserver) }
    }

    var paneRadius: CGFloat { gutter > 0 ? DesktopRadius.sheet : 0 }

    func configure(_ config: UIConfig, wallpaper: WallpaperImage?) {
        self.config = config
        self.wallpaper = wallpaper
        let wanted = Self.wallpaper(config, image: wallpaper) != nil
        if active != wanted { active = wanted }
        if desktop != (config.background == .desktop) { desktop = config.background == .desktop }
        let gutter = wanted ? CGFloat(config.gutter) : 0
        if self.gutter != gutter { self.gutter = gutter }
        if border != config.border { border = config.border }
        if composerOpacity != config.composerOpacity { composerOpacity = config.composerOpacity }
        if !wanted { image = nil; rendered = nil }
        schedule()
    }

    func windowChanged(_ window: NSWindow) {
        self.window = window
        let size = window.contentView?.bounds.size ?? .zero
        if windowSize != size { windowSize = size }
        scale = window.backingScaleFactor
        dark = DesktopPalette.isDark(window.effectiveAppearance)
        if desktop {
            window.isOpaque = false
            window.backgroundColor = .clear
        } else if !window.isOpaque {
            window.isOpaque = true
            window.backgroundColor = .windowBackgroundColor
        }
        schedule()
    }

    func report(_ key: UUID, id: String, rect: CGRect, region: Region) {
        if let current = regions[key], current.id == id, current.rect == rect, current.region == region { return }
        regions[key] = (id, rect, region)
        schedule()
    }

    func remove(_ key: UUID) {
        probes[key] = nil
        guard regions.removeValue(forKey: key) != nil else { return }
        schedule()
    }

    /// Keeps the view that reports a region, so a render measures it again:
    /// a probe hears about its own moves, not its ancestors'.
    func attach(_ key: UUID, probe: WindowRectReader.RectProbe) {
        probes[key] = WeakProbe(probe: probe)
    }

    private func remeasure() {
        for (key, weak) in probes {
            guard let rect = weak.probe?.measure(), let current = regions[key] else { continue }
            regions[key] = (current.id, rect, current.region)
        }
    }

    /// While the window is being resized the backdrop keeps up at one
    /// point per pixel, one render at a time, so it never shows an image
    /// laid out for another size for longer than a render takes; the full
    /// resolution follows when the resize ends.
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
        let image = await Task.detached(priority: .userInitiated) { BackdropRenderer.render(spec) }.value
        rendering = false
        if let image {
            rendered = spec
            self.image = image
        }
        if pending {
            pending = false
            schedule()
        }
    }

    private func spec() -> BackdropSpec? {
        remeasure()
        guard windowSize.width > 0, windowSize.height > 0, let wallpaper = Self.wallpaper(config, image: wallpaper) else { return nil }
        let appearance = NSAppearance(named: dark ? .darkAqua : .aqua)!
        let ground = DesktopPalette.ground(dark: dark)
        let roles = DesktopPalette.textRoles(dark: dark) + [DesktopPalette.readableAccent(in: appearance)]
        let paneTint: AccentContrast.RGB
        switch config.glassTint {
        case "ground": paneTint = ground
        case "accent": paneTint = DesktopPalette.accentRGB(in: appearance)
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
        let entries = regions.sorted { ($0.value.id, $0.key.uuidString) < ($1.value.id, $1.key.uuidString) }.map(\.value)
        let panes: [BackdropSpec.Pane] = entries.compactMap { entry in
            let id = entry.id
            guard case .pane(let role) = entry.region else { return nil }
            switch role {
            case .sidebar:
                return BackdropSpec.Pane(id: id, rect: entry.rect.insetBy(dx: gutter, dy: gutter), radius: paneRadius, minimum: Float(config.sidebarOpacity), strength: Float(config.backgroundOpacity))
            case .content:
                return BackdropSpec.Pane(id: id, rect: entry.rect.insetBy(dx: gutter, dy: gutter), radius: paneRadius, minimum: Float(config.contentOpacity), strength: Float(config.backgroundSessionOpacity))
            case .chrome:
                guard config.chromeOpacity > 0 else { return nil }
                return BackdropSpec.Pane(id: id, rect: entry.rect, radius: 0, minimum: Float(config.chromeOpacity), strength: Float(config.backgroundOpacity), sharp: true)
            }
        }.sorted { $0.id < $1.id }
        let texts = entries.filter { $0.region == .text || $0.region == .pane(.chrome) }.map(\.rect)
        let paneTexts = entries.filter { $0.region == .text }.map(\.rect)
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
            panes: panes,
            texts: texts,
            paneTexts: paneTexts,
            solidPanes: !config.glass || workspace.accessibilityDisplayShouldReduceTransparency,
            readability: config.readability || workspace.accessibilityDisplayShouldIncreaseContrast
        )
    }

    private static func rgb(_ c: AccentContrast.RGB) -> BackdropRGB {
        BackdropRGB(Float(c.r), Float(c.g), Float(c.b))
    }

    private static func rgb(_ c: UIConfig.RGBColor) -> BackdropRGB {
        BackdropRGB(Float(c.red), Float(c.green), Float(c.blue))
    }

    private static func wallpaper(_ config: UIConfig, image: WallpaperImage?) -> BackdropSpec.Wallpaper? {
        switch config.background {
        case .none: return nil
        case .desktop: return .desktop
        case .image(let path): return image?.id == path ? .image(image!) : nil
        case .linear(let angle, let stops): return .linear(angle: Float(angle), stops: stops.map { BackdropStop(color: rgb($0.color), location: Float($0.location)) })
        case .mesh(let columns, let rows, let colors): return .mesh(columns: columns, rows: rows, colors: colors.map(rgb))
        }
    }
}

private struct WindowBackdropKey: EnvironmentKey {
    static let defaultValue: WindowBackdrop? = nil
}

extension EnvironmentValues {
    var windowBackdrop: WindowBackdrop? {
        get { self[WindowBackdropKey.self] }
        set { self[WindowBackdropKey.self] = newValue }
    }
}

/// Gives a window its backdrop: the image every surface in it draws a
/// slice of, rendered from ui.conf and the surfaces the window reports.
struct WindowBackdropHost: ViewModifier {
    @ObservedObject var interface: InterfaceStore
    @StateObject private var backdrop = WindowBackdrop()

    func body(content: Content) -> some View {
        content
            .background {
                ZStack {
                    if backdrop.desktop { DesktopBlur() }
                    BackdropSlice(backdrop: backdrop, paneID: nil, fallback: DesktopPalette.window)
                    WindowRectReader { _, window in backdrop.windowChanged(window) }
                }
                .ignoresSafeArea()
            }
            .environment(\.windowBackdrop, backdrop)
            .onAppear { backdrop.configure(interface.config, wallpaper: interface.wallpaper) }
            .onChange(of: interface.config) { backdrop.configure(interface.config, wallpaper: interface.wallpaper) }
            .onChange(of: interface.wallpaper) { backdrop.configure(interface.config, wallpaper: interface.wallpaper) }
    }
}

extension View {
    func windowBackdrop(_ interface: InterfaceStore) -> some View {
        modifier(WindowBackdropHost(interface: interface))
    }

    /// A pane: glass over the backdrop that keeps all text in it readable.
    func glassPane(_ id: String, _ role: WindowBackdrop.PaneRole, fallback: Color) -> some View {
        modifier(GlassPane(id: id, region: .pane(role), fallback: fallback))
    }

    /// The backdrop under this view fading in from transparent at the top,
    /// so content scrolls out of sight beneath it.
    func backdropFade(_ fallback: Color, paneID: String, solidFrom: CGFloat) -> some View {
        modifier(BackdropFade(fallback: fallback, paneID: paneID, solidFrom: solidFrom))
    }

    /// Floating glass for controls over scrolling content, or the solid
    /// surface without a backdrop.
    func floatingGlass<S: InsettableShape>(_ fallback: Color, in shape: S) -> some View {
        modifier(FloatingGlass(fallback: fallback, shape: shape))
    }

    /// A surface that shows the backdrop as it is under it, so content
    /// scrolling beneath it stays hidden.
    func backdropSurface(_ fallback: Color, paneID: String? = nil) -> some View {
        modifier(BackdropSurface(fallback: fallback, paneID: paneID))
    }

    /// Text over an open area, which the scrim keeps readable.
    func scrimText(_ id: String) -> some View {
        modifier(ScrimText(id: id))
    }
}

private struct GlassPane: ViewModifier {
    let id: String
    let region: WindowBackdrop.Region
    let fallback: Color
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop, region == .pane(.chrome) {
            content.background {
                ZStack {
                    BackdropSlice(backdrop: backdrop, paneID: id, fallback: fallback)
                    RegionReader(id: id, region: region, backdrop: backdrop)
                }
                .ignoresSafeArea()
            }
        } else if let backdrop {
            GlassPaneBody(content: content, id: id, region: region, fallback: fallback, backdrop: backdrop)
        } else {
            content.background(fallback)
        }
    }
}

private struct GlassPaneBody<Content: View>: View {
    let content: Content
    let id: String
    let region: WindowBackdrop.Region
    let fallback: Color
    @ObservedObject var backdrop: WindowBackdrop

    var body: some View {
        content
            .padding(backdrop.gutter)
            .background {
                ZStack {
                    BackdropSlice(backdrop: backdrop, paneID: id, fallback: fallback)
                    if backdrop.active, backdrop.border > 0 {
                        RoundedRectangle(cornerRadius: backdrop.paneRadius)
                            .strokeBorder(DesktopPalette.text.opacity(backdrop.border), lineWidth: 1)
                            .padding(backdrop.gutter)
                    }
                    RegionReader(id: id, region: region, backdrop: backdrop)
                }
                .ignoresSafeArea()
            }
    }
}

private struct BackdropSurface: ViewModifier {
    let fallback: Color
    let paneID: String?
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop {
            content.background { BackdropSlice(backdrop: backdrop, paneID: paneID, fallback: fallback).ignoresSafeArea() }
        } else {
            content.background(fallback)
        }
    }
}

private struct BackdropFade: ViewModifier {
    let fallback: Color
    let paneID: String
    let solidFrom: CGFloat
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        let fade = LinearGradient(colors: [.clear, .black], startPoint: .top, endPoint: UnitPoint(x: 0.5, y: solidFrom))
        if let backdrop {
            content.background {
                BackdropSlice(backdrop: backdrop, paneID: paneID, fallback: fallback)
                    .mask(fade)
            }
        } else {
            content.background(fallback.mask(fade).allowsHitTesting(false))
        }
    }
}

private struct FloatingGlass<S: InsettableShape>: ViewModifier {
    let fallback: Color
    let shape: S
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop {
            FloatingGlassBody(content: content, fallback: fallback, shape: shape, backdrop: backdrop)
        } else {
            content.background(fallback, in: shape)
        }
    }
}

private struct FloatingGlassBody<Content: View, S: InsettableShape>: View {
    let content: Content
    let fallback: Color
    let shape: S
    @ObservedObject var backdrop: WindowBackdrop
    @Environment(\.accessibilityReduceTransparency) private var reduceTransparency

    var body: some View {
        if !backdrop.active || reduceTransparency {
            content.background(fallback, in: shape)
        } else if #available(macOS 26, *) {
            content.glassEffect(.regular.tint(DesktopPalette.window.opacity(backdrop.composerOpacity)), in: shape)
        } else {
            content
                .background(DesktopPalette.window.opacity(backdrop.composerOpacity), in: shape)
                .background(.ultraThinMaterial, in: shape)
        }
    }
}

/// One view's claim on a backdrop region, for as long as its reader is in
/// the window.
@MainActor
final class RegionHandle {
    let key = UUID()
    let id: String
    let region: WindowBackdrop.Region
    private weak var backdrop: WindowBackdrop?

    init(id: String, region: WindowBackdrop.Region, backdrop: WindowBackdrop) {
        self.id = id
        self.region = region
        self.backdrop = backdrop
    }

    func attach(_ probe: WindowRectReader.RectProbe) {
        backdrop?.attach(key, probe: probe)
    }

    func report(_ rect: CGRect) {
        backdrop?.report(key, id: id, rect: rect, region: region)
    }

    func leave() {
        backdrop?.remove(key)
    }
}

struct RegionReader: View {
    @State private var handle: RegionHandle

    init(id: String, region: WindowBackdrop.Region, backdrop: WindowBackdrop) {
        _handle = State(initialValue: RegionHandle(id: id, region: region, backdrop: backdrop))
    }

    var body: some View {
        WindowRectReader(probe: { handle.attach($0) }, onLeave: { handle.leave() }) { rect, _ in handle.report(rect) }
    }
}

private struct ScrimText: ViewModifier {
    let id: String
    @Environment(\.windowBackdrop) private var backdrop

    func body(content: Content) -> some View {
        if let backdrop {
            content.background { RegionReader(id: id, region: .text, backdrop: backdrop) }
        } else {
            content
        }
    }
}

/// The part of the window's backdrop under this view, or the plain
/// surface colour when there is no backdrop.
struct BackdropSlice: View {
    @ObservedObject var backdrop: WindowBackdrop
    let paneID: String?
    let fallback: Color
    @State private var origin: CGPoint = .zero

    var body: some View {
        Color.clear
            .overlay(alignment: .topLeading) {
                if backdrop.active, let rendered = backdrop.image {
                    Image(decorative: rendered.image, scale: CGFloat(rendered.image.width) / max(rendered.size.width, 1))
                        .resizable()
                        .frame(width: backdrop.windowSize.width, height: backdrop.windowSize.height)
                        .offset(x: -origin.x, y: -origin.y)
                }
            }
            .overlay {
                if !backdrop.active || backdrop.image == nil {
                    fallback
                }
            }
            .clipped()
            .background { WindowRectReader { rect, _ in if origin != rect.origin { origin = rect.origin } } }
            .allowsHitTesting(false)
            .accessibilityHidden(true)
    }
}

private struct DesktopBlur: NSViewRepresentable {
    func makeNSView(context: Context) -> NSVisualEffectView {
        let view = NSVisualEffectView()
        view.blendingMode = .behindWindow
        view.material = .underWindowBackground
        view.state = .active
        return view
    }

    func updateNSView(_ view: NSVisualEffectView, context: Context) {}
}
