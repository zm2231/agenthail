import XCTest

extension XCUIApplication {
    var isPad: Bool { UIDevice.current.userInterfaceIdiom == .pad }

    func tab(_ name: String) -> XCUIElement {
        isPad ? buttons.matching(NSPredicate(format: "label == %@", name)).firstMatch : tabBars.buttons[name]
    }

    var sessionList: XCUIElement { descendants(matching: .any)["session-list"] }

    func backToSessions(file: StaticString = #filePath, line: UInt = #line) {
        let bar = navigationBars.buttons
        guard bar["Show Sidebar"].exists || bar["Hide Sidebar"].exists else {
            let started = Date()
            while sessionList.exists {
                if Date().timeIntervalSince(started) > 5 { return }
                RunLoop.current.run(until: Date().addingTimeInterval(0.1))
            }
            bar.element(boundBy: 0).tapWhenReady(file: file, line: line)
            return
        }
        if sessionList.exists, sessionList.isHittable { return }
        let show = bar["Show Sidebar"]
        if show.exists, show.isHittable { show.tap() }
    }
}
