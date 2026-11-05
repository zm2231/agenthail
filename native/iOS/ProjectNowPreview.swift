#if DEBUG
import Foundation

enum ProjectNowPreview {
    static let projectID = "repo:github.com/demo/fieldnotes"
    private static var project: [String: Any] { ["id": projectID, "displayName": "fieldnotes", "rootPath": "/work/fieldnotes", "kind": "git"] }

    static var scenario: String? { UserDefaults.standard.string(forKey: "preview-now") }

    private static func ago(_ minutes: Double) -> String { ISO8601DateFormatter().string(from: Date().addingTimeInterval(-minutes * 60)) }

    private static func checkout(_ id: String, _ path: String, branch: String? = nil, dirty: Bool = false) -> [String: Any] {
        var checkout: [String: Any] = ["id": id, "path": path, "isMain": branch == "main", "dirty": dirty]
        if let branch { checkout["branch"] = branch }
        return checkout
    }

    private static func row(_ id: String, _ name: String, status: String = "idle", minutes: Double, checkout: [String: Any], capabilities: [String: Bool], extra: [String: Any] = [:]) -> [String: Any] {
        var row: [String: Any] = ["id": id, "name": name, "surface": "claude", "status": status, "lastActive": ago(minutes), "queueCount": 0, "open": true, "current": true, "capabilities": capabilities, "project": project, "cwd": checkout["path"] as? String ?? "", "checkout": checkout]
        row.merge(extra) { $1 }
        return row
    }

    static func sessions(capabilities: [String: Bool]) -> [[String: Any]] {
        let main = checkout("checkout-main", "/work/fieldnotes", branch: "main")
        let search = checkout("checkout-search", "/work/fieldnotes/.worktrees/search", branch: "fix/search", dirty: true)
        let export = checkout("checkout-export", "/work/fieldnotes/.worktrees/export", branch: "feat/export")
        let waiting: [String: Any] = ["waitingOn": SessionPreview.previewWaiting]
        switch scenario {
        case "empty":
            return []
        case "single":
            return [
                row("idle", "Update the docs", minutes: 40, checkout: main, capabilities: capabilities),
                row("waiting", "Index titles", status: "busy", minutes: 4, checkout: main, capabilities: capabilities, extra: waiting),
            ]
        case "plain":
            let notes = checkout("checkout-notes", "/home/demo/notes")
            return [
                row("notes", "Sort the field notes", status: "busy", minutes: 3, checkout: notes, capabilities: capabilities),
                row("scans", "Rename the photos", minutes: 90, checkout: checkout("checkout-scans", "/home/demo/scans"), capabilities: capabilities),
            ]
        case "unreachable":
            let far: [String: Any] = ["id": "buildhost", "name": "build-server.example.net", "state": "unreachable"]
            return [
                row("near", "Update the docs", minutes: 40, checkout: main, capabilities: capabilities),
                row("buildhost::far", "Run the nightly build", status: "busy", minutes: 2, checkout: checkout("buildhost::checkout-main", "/srv/fieldnotes", branch: "main", dirty: true), capabilities: capabilities, extra: ["host": far, "unavailableReason": "build-server.example.net is offline"]),
            ]
        default:
            return [
                row("docs", "Update the docs", minutes: 40, checkout: main, capabilities: capabilities),
                row("flake", "Trace the flake", minutes: 180, checkout: main, capabilities: capabilities),
                row("csv", "Add CSV export", status: "busy", minutes: 3, checkout: export, capabilities: capabilities),
                row("build", "Check the build", status: "busy", minutes: 11, checkout: search, capabilities: capabilities, extra: ["subagents": ["count": 2, "working": 2]]),
                row("waiting", "Index titles", status: "busy", minutes: 4, checkout: search, capabilities: capabilities, extra: waiting),
            ]
        }
    }

    static var checkouts: [[String: Any]] {
        var checkouts: [[String: Any]] = [["host": ["id": "computer", "name": "computer", "state": "local"], "projectId": "implicit:/work/fieldnotes", "rootPath": "/work/fieldnotes", "added": true]]
        if scenario == "unreachable" {
            checkouts.append(["host": ["id": "buildhost", "name": "build-server.example.net", "state": "unreachable"], "projectId": "buildhost::implicit:/srv/fieldnotes", "rootPath": "/srv/fieldnotes"])
        }
        return checkouts
    }
}
#endif
