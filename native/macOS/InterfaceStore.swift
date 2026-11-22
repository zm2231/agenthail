import AppKit
import Foundation
import os

@MainActor
final class InterfaceStore: ObservableObject {
    @Published private(set) var config = UIConfig.defaults
    /// Decoded images for the dark and light backgrounds, by path.
    @Published private(set) var wallpapers: [String: WallpaperImage] = [:]
    @Published private(set) var fallbacks: [Bool: WallpaperImage] = [:]
    /// False while the current config's images are still loading.
    @Published private(set) var settled = true
    @Published private(set) var problem: String?
    @Published private(set) var history: [UIChangeEntry] = []
    private var base = UIConfig.defaults
    private var overlay: [String: UIConfig.Value] = [:]
    private var loadTask: Task<Void, Never>?
    private var textScaleWrite: Task<Void, Never>?
    private var generation = 0
    private static let signposter = OSSignposter(subsystem: "com.agenthail.app", category: "Appearance")

    static var device: String { Host.current().localizedName ?? "" }

    var drawn: [Bool: WallpaperImage] {
        var drawn: [Bool: WallpaperImage] = [:]
        for dark in [true, false] {
            guard case .image(let path) = config.background(dark: dark) else { continue }
            drawn[dark] = wallpapers[path] ?? fallbacks[dark]
        }
        return drawn
    }

    func load(from api: AgenthailAPI) {
        loadTask?.cancel()
        generation += 1
        let current = generation
        loadTask = Task {
            do {
                let loaded = try await api.uiConfig(device: Self.device, kind: "mac")
                if Task.isCancelled || current != generation { return }
                let problem = await adopt(loaded, api: api, generation: current)
                if Task.isCancelled || current != generation { return }
                self.problem = problem
            } catch {
                if Task.isCancelled || error.isCancellation || current != generation { return }
                settled = true
                problem = AgenthailAPIError.userMessage(error)
            }
        }
    }

    func snapshotProblem(for rev: String) -> String? {
        guard config.rev == rev else { return "The appearance change \(rev) didn't arrive in time." }
        guard settled else { return "The background image didn't load in time." }
        guard config.imagePaths.allSatisfy({ wallpapers[$0] != nil }) else { return problem ?? "The background image couldn't be shown." }
        return nil
    }

    func captureFailure(for rev: String?, backdrop: WindowBackdrop?) -> WindowImage.Failure? {
        guard let rev, !rev.isEmpty else { return nil }
        if let failure = revisionFailure(for: rev) { return failure }
        guard snapshotProblem(for: rev) == nil, backdrop?.shows(self) == true else { return .undrawn }
        return nil
    }

    func revisionFailure(for rev: String?) -> WindowImage.Failure? {
        guard let rev, !rev.isEmpty, config.rev != rev else { return nil }
        return .changed
    }

    /// Shows a value at once, before it is written, while a slider moves.
    func preview(_ key: String, _ value: UIConfig.Value) {
        overlay[key] = value
        publish()
    }

    func dropPreview() {
        overlay = [:]
        publish()
    }

    /// Sends one change; the daemon's answer replaces any preview. Returns the
    /// daemon's reason when it refuses the change.
    @discardableResult
    func change(_ change: UIChange, api: AgenthailAPI) async -> String? {
        do {
            let loaded = try await api.changeUI(change, device: Self.device)
            loadTask?.cancel()
            generation += 1
            let current = generation
            overlay = [:]
            let problem = await adopt(loaded, api: api, generation: current)
            if current == generation, !Task.isCancelled { self.problem = problem }
            await loadHistory(api: api)
            return nil
        } catch {
            overlay = [:]
            publish()
            return AgenthailAPIError.userMessage(error)
        }
    }

    func loadHistory(api: AgenthailAPI) async {
        if let entries = try? await api.uiLog(limit: 20) { history = entries }
    }

    private func adopt(_ loaded: UIConfig, api: AgenthailAPI, generation current: Int) async -> String? {
        let signpost = Self.signposter.makeSignpostID()
        let interval = Self.signposter.beginInterval("Appearance load", id: signpost)
        defer {
            Self.signposter.endInterval("Appearance load", interval)
            if current == generation { settled = true }
        }
        let active = Self.prefersDark(loaded)
        var next: [Bool: WallpaperImage] = [:]
        for dark in [true, false] {
            guard case .image(let path) = loaded.background(dark: dark), wallpapers[path] == nil else { continue }
            next[dark] = drawn[dark]
        }
        let kept = wallpapers.filter { loaded.imagePaths.contains($0.key) }
        if kept != wallpapers { wallpapers = kept }
        if next != fallbacks { fallbacks = next }
        base = loaded
        settled = false
        publish()
        Self.signposter.emitEvent("Config applied", id: signpost)
        var problem: String?
        let maxPixels = Int(NSScreen.screens.map { max($0.frame.width, $0.frame.height) * $0.backingScaleFactor }.max() ?? 2880)
        for path in Self.fetchOrder(loaded, dark: active) where wallpapers[path] == nil {
            do {
                let data = try await api.uiBackground(path: path)
                let decoded = await Task.detached(operation: { WallpaperImage.decode(data, id: path, maxPixels: maxPixels) }).value
                guard current == generation, !Task.isCancelled else { return problem }
                guard let decoded else {
                    problem = "The background image \(path) can't be read."
                    continue
                }
                wallpapers[path] = decoded
                for dark in [true, false] where loaded.background(dark: dark) == .image(path: path) { fallbacks[dark] = nil }
                let side = loaded.background(dark: active) == .image(path: path) ? "active" : "inactive"
                Self.signposter.emitEvent("Wallpaper applied", id: signpost, "\(side, privacy: .public)")
            } catch {
                if current != generation || Task.isCancelled || error.isCancellation { return problem }
                problem = AgenthailAPIError.userMessage(error)
            }
        }
        return problem
    }

    static func prefersDark(_ config: UIConfig) -> Bool {
        switch config.theme {
        case "dark": return true
        case "light": return false
        default: return NSApp?.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua
        }
    }

    static func fetchOrder(_ config: UIConfig, dark: Bool) -> [String] {
        var paths: [String] = []
        for case .image(let path) in [config.background(dark: dark), config.background(dark: !dark)] where !paths.contains(path) { paths.append(path) }
        return paths
    }

    private func publish() {
        let next = AppearanceDraft.preview(base, overlay: overlay)
        switch next.theme {
        case "dark": NSApp.appearance = NSAppearance(named: .darkAqua)
        case "light": NSApp.appearance = NSAppearance(named: .aqua)
        default: NSApp.appearance = nil
        }
        if next != config { config = next }
        let scale = TextScale.clamped(CGFloat(next.textScale))
        if TextScale.shared.factor != scale { TextScale.shared.factor = scale }
    }

    /// Applies a text size at once and writes it for this Mac after a second without another change, so a run of key presses is one change.
    func stepTextScale(by steps: Int?, api: AgenthailAPI?) {
        let next = steps.map { TextScale.stepped(TextScale.shared.factor, by: $0) } ?? 1
        preview("text.scale", .number(Double(next)))
        textScaleWrite?.cancel()
        guard let api else { return }
        textScaleWrite = Task { [weak self] in
            try? await Task.sleep(for: .seconds(1))
            guard let self, !Task.isCancelled else { return }
            await self.change(Self.textScaleChange(next, device: Self.device), api: api)
        }
    }

    func supersedeTextScaleWrite(by change: UIChange) {
        guard ((change.set ?? []) + (change.unset ?? [])).contains(where: { $0.hasPrefix("text.scale") }) else { return }
        textScaleWrite?.cancel()
        textScaleWrite = nil
    }

    static func textScaleChange(_ scale: CGFloat, device: String) -> UIChange {
        let place = device.isEmpty ? "mac" : "device:" + device
        let percent = Int((scale * 100).rounded())
        return UIChange(action: "set", set: ["text.scale@\(place) = \(Double(percent) / 100)"], message: "Text size \(percent)%")
    }
}
