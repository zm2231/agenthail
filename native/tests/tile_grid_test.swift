@testable import AgenthailHelpers
import AppKit

@main
struct TileGridTest {
    static func main() {
        let padding: CGFloat = 16
        let font = NSFont.systemFont(ofSize: 11)
        for (sidebar, expected) in [(CGFloat(248), 4), (300, 5), (340, 6)] {
            let columns = TileGrid.columns(width: sidebar - padding)
            check(columns == expected, "a \(sidebar) pt sidebar fits \(expected) columns, got \(columns)")
            let tile = (sidebar - padding - CGFloat(columns - 1) * TileGrid.spacing) / CGFloat(columns)
            for name in ["backend", "mobile1", "website", "monitor", "Inbox", "All", "+10"] {
                let label = (name as NSString).size(withAttributes: [.font: font]).width
                check(label <= tile, "\(name) (\(label) pt) fits a \(tile) pt tile at \(sidebar) pt")
            }
        }
        check(TileGrid.columns(width: 0) == 3 && TileGrid.columns(width: 120) == 3, "there are always at least three columns")

        let projects = (0..<15).map { n in
            ProjectState(id: "p\(n)", displayName: "p\(n)", activity: ProjectActivity(id: "p\(n)", lastActive: "2026-11-08T10:00:00Z", sessionCount: 15 - n, checkouts: []))
        }
        for columns in [3, 4, 5, 6] {
            let tiles = TileGrid.fill(columns: columns) { ProjectTiles.order(projects, limit: $0) }
            let count = 2 + tiles.shown.count + (tiles.more.isEmpty ? 0 : 1)
            check(count == TileGrid.rows * columns, "\(columns) columns: Inbox, projects, more and All fill both rows, got \(count)")
            check(tiles.shown.map(\.id) == ProjectTiles.order(projects, limit: tiles.shown.count).shown.map(\.id), "filling keeps the tile order")
        }
        let mostlyIdle = projects.enumerated().map { index, project in
            ProjectState(id: project.id, displayName: project.displayName, activity: ProjectActivity(id: project.id, lastActive: nil, sessionCount: index < 2 ? 1 : 0, checkouts: []))
        }
        for columns in [3, 4, 5, 6] {
            let tiles = TileGrid.fill(columns: columns) { ProjectTiles.order(mostlyIdle, limit: $0) }
            check(2 + tiles.shown.count + 1 == TileGrid.rows * columns && Array(tiles.shown.prefix(2).map(\.id)) == ["p0", "p1"], "\(columns) columns: idle projects fill the rows after the active ones")
        }
        let few = TileGrid.fill(columns: 6) { ProjectTiles.order(Array(projects.prefix(10)), limit: $0) }
        check(few.shown.count == 10 && few.more.isEmpty, "every project shows when they all fit, with no more tile")
        let eleven = TileGrid.fill(columns: 6) { ProjectTiles.order(Array(projects.prefix(11)), limit: $0) }
        check(eleven.shown.count == 9 && eleven.more.count == 2, "one too many gives up a project tile for the more tile")
        print("ok")
    }

    static func check(_ condition: Bool, _ message: String) {
        if !condition {
            print("FAIL: \(message)")
            exit(1)
        }
    }
}
