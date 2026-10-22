import AppKit
import Foundation

@MainActor
final class InterfaceStore: ObservableObject {
    @Published private(set) var config = UIConfig.defaults
    @Published private(set) var wallpaper: WallpaperImage?
    @Published private(set) var problem: String?
    private var loadTask: Task<Void, Never>?

    func load(from api: AgenthailAPI) {
        loadTask?.cancel()
        loadTask = Task {
            do {
                let loaded = try await api.uiConfig(device: Host.current().localizedName ?? "")
                if Task.isCancelled { return }
                var problem: String?
                if case .image(let path) = loaded.background, wallpaper?.id != path {
                    let data = try await api.uiBackground(path: path)
                    let maxPixels = Int(NSScreen.screens.map { max($0.frame.width, $0.frame.height) * $0.backingScaleFactor }.max() ?? 2880)
                    let decoded = await Task.detached { WallpaperImage.decode(data, id: path, maxPixels: maxPixels) }.value
                    if Task.isCancelled { return }
                    wallpaper = decoded
                    if decoded == nil { problem = "The background image \(path) can't be read." }
                }
                apply(loaded)
                self.problem = problem
            } catch {
                if Task.isCancelled || error.isCancellation { return }
                problem = AgenthailAPIError.userMessage(error)
            }
        }
    }

    private func apply(_ loaded: UIConfig) {
        switch loaded.theme {
        case "dark": NSApp.appearance = NSAppearance(named: .darkAqua)
        case "light": NSApp.appearance = NSAppearance(named: .aqua)
        default: NSApp.appearance = nil
        }
        DesktopPalette.choose(accent: loaded.accent.map { AccentContrast.RGB(r: $0.red, g: $0.green, b: $0.blue) })
        if loaded != config { config = loaded }
    }
}
