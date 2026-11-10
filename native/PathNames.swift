import Foundation

enum PathNames {
    static func components(_ path: String) -> [String] {
        path.split(separator: "/").map(String.init).filter { $0 != "." }
    }

    static func last(_ path: String) -> String {
        components(path).last ?? "/"
    }
}
