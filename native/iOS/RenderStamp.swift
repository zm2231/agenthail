import Foundation

enum RenderStamp {
    static let current: String = {
        let version = Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? ""
        let built = Bundle.main.executableURL
            .flatMap { try? FileManager.default.attributesOfItem(atPath: $0.path)[.modificationDate] as? Date }
            .map { String(Int($0.timeIntervalSince1970)) } ?? ""
        return "r\(version)_\(built)"
    }()

    static func prune(_ directory: URL) {
        let files = FileManager.default
        for file in (try? files.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)) ?? []
        where file.pathExtension == "png" && !file.lastPathComponent.hasPrefix(current + "-") {
            try? files.removeItem(at: file)
        }
    }
}
