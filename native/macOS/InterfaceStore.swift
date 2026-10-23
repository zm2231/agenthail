import AppKit
import Foundation

@MainActor
final class InterfaceStore: ObservableObject {
    @Published private(set) var config = UIConfig.defaults
    /// Decoded images for the dark and light backgrounds, by path.
    @Published private(set) var wallpapers: [String: WallpaperImage] = [:]
    @Published private(set) var problem: String?
    @Published private(set) var history: [UIChangeEntry] = []
    private var base = UIConfig.defaults
    private var overlay: [String: UIConfig.Value] = [:]
    private var loadTask: Task<Void, Never>?

    static var device: String { Host.current().localizedName ?? "" }

    func load(from api: AgenthailAPI) {
        loadTask?.cancel()
        loadTask = Task {
            do {
                let loaded = try await api.uiConfig(device: Self.device)
                if Task.isCancelled { return }
                let problem = await adopt(loaded, api: api)
                if Task.isCancelled { return }
                self.problem = problem
            } catch {
                if Task.isCancelled || error.isCancellation { return }
                problem = AgenthailAPIError.userMessage(error)
            }
        }
    }

    /// Shows a value at once, before it is written, while a slider moves.
    func preview(_ key: String, _ value: UIConfig.Value) {
        overlay[key] = value
        publish()
    }

    /// Sends one change; the daemon's answer replaces any preview. Returns the
    /// daemon's reason when it refuses the change.
    @discardableResult
    func change(_ change: UIChange, api: AgenthailAPI) async -> String? {
        do {
            let loaded = try await api.changeUI(change, device: Self.device)
            loadTask?.cancel()
            overlay = [:]
            problem = await adopt(loaded, api: api)
            await loadHistory(api: api)
            return nil
        } catch {
            overlay = [:]
            publish()
            return AgenthailAPIError.userMessage(error)
        }
    }

    func upload(_ data: Data, name: String, api: AgenthailAPI) async throws -> String {
        try await api.uploadBackground(data, name: name)
    }

    func loadHistory(api: AgenthailAPI) async {
        if let entries = try? await api.uiLog(limit: 20) { history = entries }
    }

    private func adopt(_ loaded: UIConfig, api: AgenthailAPI) async -> String? {
        var problem: String?
        var images: [String: WallpaperImage] = [:]
        let maxPixels = Int(NSScreen.screens.map { max($0.frame.width, $0.frame.height) * $0.backingScaleFactor }.max() ?? 2880)
        for path in loaded.imagePaths {
            if let kept = wallpapers[path] {
                images[path] = kept
                continue
            }
            do {
                let data = try await api.uiBackground(path: path)
                if let decoded = await Task.detached(operation: { WallpaperImage.decode(data, id: path, maxPixels: maxPixels) }).value {
                    images[path] = decoded
                } else {
                    problem = "The background image \(path) can't be read."
                }
            } catch {
                problem = AgenthailAPIError.userMessage(error)
            }
        }
        if images != wallpapers { wallpapers = images }
        base = loaded
        publish()
        return problem
    }

    private func publish() {
        let next = AppearanceDraft.preview(base, overlay: overlay)
        switch next.theme {
        case "dark": NSApp.appearance = NSAppearance(named: .darkAqua)
        case "light": NSApp.appearance = NSAppearance(named: .aqua)
        default: NSApp.appearance = nil
        }
        DesktopPalette.choose(accent: next.accent.map { AccentContrast.RGB(r: $0.red, g: $0.green, b: $0.blue) })
        if next != config { config = next }
    }
}
