import Foundation

/// The view a daemon would send with preview rows: every family in the order
/// the rows are served, so previews and UI tests render a daemon order.
enum PreviewOrder {
    static func pinned(_ row: [String: Any]) -> Bool {
        row["status"] as? String == "busy" || (row["queueCount"] as? Int ?? 0) > 0 || row["waitingOn"] != nil
    }

    static func scoped(_ json: String, _ scope: String?) -> String {
        guard scope == "running", var object = (try? JSONSerialization.jsonObject(with: Data(json.utf8))) as? [String: Any],
              let sessions = object["sessions"] as? [[String: Any]] else { return withView(json) }
        object["sessions"] = sessions.filter(pinned)
        guard let data = try? JSONSerialization.data(withJSONObject: object) else { return withView(json) }
        return withView(String(decoding: data, as: UTF8.self))
    }

    static func withView(_ json: String) -> String {
        guard var object = (try? JSONSerialization.jsonObject(with: Data(json.utf8))) as? [String: Any], object["view"] == nil,
              let sessions = object["sessions"] as? [[String: Any]] else { return json }
        let ids = Set(sessions.compactMap { $0["id"] as? String })
        func parent(_ row: [String: Any]) -> String? {
            ((row["subagent"] as? [String: Any])?["parentId"] as? String).flatMap { ids.contains($0) ? $0 : nil }
        }
        var families: [[String: Any]] = []
        var projects: [(id: String, name: String, families: [String])] = []
        for root in sessions where parent(root) == nil {
            guard let id = root["id"] as? String else { continue }
            var members: [[String: Any]] = []
            func walk(_ at: String, depth: Int) {
                guard let row = sessions.first(where: { $0["id"] as? String == at }) else { return }
                members.append(["id": at, "depth": depth, "contextOnly": false, "pinned": pinned(row)])
                for child in sessions where parent(child) == at { walk(child["id"] as? String ?? "", depth: depth + 1) }
            }
            walk(id, depth: 0)
            let project = root["project"] as? [String: Any]
            let projectID = project?["id"] as? String ?? ""
            let checkoutID = (root["checkout"] as? [String: Any])?["id"] as? String ?? ""
            families.append(["id": id, "rootId": id, "projectId": projectID, "checkoutId": checkoutID, "members": members, "pinned": members.contains { $0["pinned"] as? Bool == true }, "workingSubagents": 0])
            if let index = projects.firstIndex(where: { $0.id == projectID }) {
                projects[index].families.append(id)
            } else {
                projects.append((projectID, project?["displayName"] as? String ?? "No project", [id]))
            }
        }
        let count = ["families": families.count, "sessions": sessions.count]
        object["view"] = [
            "counts": ["running": count, "current": count, "recent": count, "all": count, "archived": ["families": 0, "sessions": 0]],
            "surfaces": [],
            "projects": projects.map { ["id": $0.id, "name": $0.name, "counts": ["families": $0.families.count, "sessions": $0.families.count], "collapsedFamilyIds": $0.families] },
            "families": families,
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: object) else { return json }
        return String(decoding: data, as: UTF8.self)
    }
}
