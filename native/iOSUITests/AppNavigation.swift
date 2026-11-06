import XCTest

extension XCUIApplication {
    var isPad: Bool { UIDevice.current.userInterfaceIdiom == .pad }

    func tab(_ name: String) -> XCUIElement {
        isPad ? buttons.matching(NSPredicate(format: "label == %@", name)).firstMatch : tabBars.buttons[name]
    }

    func backToSessions(file: StaticString = #filePath, line: UInt = #line) {
        guard isPad else {
            navigationBars.buttons.element(boundBy: 0).tapWhenReady(file: file, line: line)
            return
        }
        let show = navigationBars.buttons["Show Sidebar"]
        if show.exists, show.isHittable { show.tap() }
    }
}
