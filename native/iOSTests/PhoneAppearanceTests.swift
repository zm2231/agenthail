import XCTest
@testable import Agenthail

@MainActor
final class PhoneAppearanceTests: XCTestCase {
    private var cache: URL!

    override func setUp() {
        super.setUp()
        cache = FileManager.default.temporaryDirectory.appendingPathComponent("appearance-\(UUID().uuidString)")
        UIConfigURLProtocol.state.reset()
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: cache)
        super.tearDown()
    }

    private func config(_ values: [String: Any], rev: String = "r1") throws -> UIConfig {
        let data = try JSONSerialization.data(withJSONObject: ["rev": rev, "values": values, "problems": []])
        return try JSONDecoder().decode(UIConfig.self, from: data)
    }

    private func key(_ surface: PhoneAppearance.Surface, target: Float = 4.5) -> PhoneAppearance.Key {
        PhoneAppearance.Key(surface: surface, size: CGSize(width: 402, height: 874), scale: 3, dark: true, solid: false, target: target)
    }

    func testLoadAsksForThisDeviceClassAndKeepsTheConfigForTheNextLaunch() async throws {
        UIConfigURLProtocol.state.body = #"{"rev":"abc","values":{"background":"gradient: 135deg, #1b2230 0%, #4a2c8a 100%","glass.opacity":0.3},"problems":[]}"#
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [UIConfigURLProtocol.self]
        let api = AgenthailAPI(baseURL: URL(string: "https://host.invalid")!, token: "t", session: URLSession(configuration: configuration))
        let appearance = PhoneAppearance(cache: cache)
        appearance.load(from: api)
        for _ in 0..<100 where appearance.config.rev != "abc" { try await Task.sleep(for: .milliseconds(20)) }

        XCTAssertEqual(appearance.config.rev, "abc")
        XCTAssertEqual(UIConfigURLProtocol.state.query["class"], PhoneAppearance.deviceKind)
        XCTAssertEqual(UIConfigURLProtocol.state.query["device"], UIDevice.current.name)
        XCTAssertTrue(appearance.active)

        let relaunched = PhoneAppearance(cache: cache)
        XCTAssertEqual(relaunched.config, appearance.config)
        XCTAssertEqual(relaunched.config.contentOpacity, 0.3)
    }

    func testEachSurfaceUsesItsOwnPaneAndStrength() throws {
        let appearance = PhoneAppearance(cache: nil)
        appearance.preview(try config([
            "background": "mesh: 2x2, #102030, #203040, #304050, #405060",
            "background.opacity": 0.8, "background.session-opacity": 0.4,
            "glass.opacity": 0.2, "glass.sidebar.opacity": 0.5, "texture": "scanlines",
        ]))

        let session = appearance.spec(key(.pane))
        XCTAssertEqual(session.panes.map(\.minimum), [0.2])
        XCTAssertEqual(session.panes.first?.rect, CGRect(x: 0, y: 0, width: 402, height: 874))
        XCTAssertEqual(session.strength, 0.4)
        XCTAssertTrue(session.texts.isEmpty)

        let list = appearance.spec(key(.list))
        XCTAssertTrue(list.panes.isEmpty, "a list shows the background itself; content floats on cards")
        XCTAssertEqual(list.strength, 0.8)
        XCTAssertEqual(list.texts.count, 1, "the title band is scrimmed")
        XCTAssertEqual(list.texture, "scanlines")

        let cards = appearance.spec(key(.cards))
        XCTAssertEqual(cards.panes.map(\.minimum), [0.5], "cards start from the sidebar tint")
        XCTAssertEqual(cards.panes.first?.rect, CGRect(x: 0, y: 0, width: 402, height: 874), "a card can scroll anywhere on screen")
        XCTAssertEqual(cards.texture, "none")
        XCTAssertEqual(cards.size, list.size)
        XCTAssertEqual(cards.blur, list.blur)
    }

    func testIncreasedContrastAndReducedTransparencyReachTheRenderer() throws {
        let appearance = PhoneAppearance(cache: nil)
        let values = try config(["background": "gradient: 90deg, #000000 0%, #ffffff 100%", "contrast.target": 5])
        appearance.preview(values)
        XCTAssertEqual(PhoneAppearance.contrastTarget(values, increased: false), 5)
        XCTAssertEqual(PhoneAppearance.contrastTarget(values, increased: true), 7)
        XCTAssertEqual(appearance.spec(key(.pane, target: 7)).target, 7)
        var solid = key(.pane)
        solid.solid = true
        XCTAssertTrue(appearance.spec(solid).solidPanes)
    }

    func testNoBackdropWithoutABackgroundTheGlassOrAPhoneDesktop() throws {
        let appearance = PhoneAppearance(cache: nil)
        for values in [["background": "none"], ["background": "desktop"], ["background": "gradient: 90deg, #000000 0%, #ffffff 100%", "glass": "off"], ["background": "image: backgrounds/missing.png"]] {
            appearance.preview(try config(values))
            XCTAssertFalse(appearance.active, "\(values)")
        }
    }

    func testRenderedBackdropsAreKeptPerConfigRevision() throws {
        let appearance = PhoneAppearance(cache: cache)
        appearance.preview(try config(["background": "mesh: 2x2, #102030, #203040, #304050, #405060"], rev: "one"))
        let first = appearance.file(key(.pane))
        appearance.preview(try config(["background": "mesh: 2x2, #102030, #203040, #304050, #405060"], rev: "two"))
        XCTAssertNotEqual(first, appearance.file(key(.pane)))
        XCTAssertNotEqual(appearance.file(key(.pane)), appearance.file(key(.list)))
    }

    func testThemeAndAccentFollowTheConfig() throws {
        let appearance = PhoneAppearance(cache: nil)
        appearance.preview(try config(["theme": "light", "theme.accent": "#336699"]))
        XCTAssertEqual(appearance.colorScheme, .light)
        appearance.preview(try config(["theme": "system"]))
        XCTAssertNil(appearance.colorScheme)
    }

    func testCardsKeepEveryTextColourReadableWithoutHidingTheBackground() throws {
        let appearance = PhoneAppearance(cache: nil)
        appearance.preview(try config([
            "background": "mesh: 3x3, #2e1972, #182c58, #0e5671, #2d1655, #c8d0e0, #2e3340, #591948, #8a3049, #682e2c",
            "glass.sidebar.opacity": 0.3,
        ]))
        let small = PhoneAppearance.Key(surface: .cards, size: CGSize(width: 200, height: 400), scale: 1, dark: true, solid: false, target: 4.5)
        let image = try XCTUnwrap(BackdropRenderer.render(appearance.spec(small))).image
        let width = image.width, height = image.height
        var pixels = [UInt8](repeating: 0, count: width * height * 4)
        let context = try XCTUnwrap(CGContext(data: &pixels, width: width, height: height, bitsPerComponent: 8, bytesPerRow: width * 4,
                                              space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue))
        context.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))

        let roles = PhonePalette.textRoles(dark: true).map { ScrimField.luminance($0.x, $0.y, $0.z) }
        let dimmest = try XCTUnwrap(roles.min())
        var worst = Float.infinity
        var shown: [Float] = []
        for i in stride(from: 0, to: pixels.count, by: 4) {
            let background = ScrimField.luminance(Float(pixels[i]) / 255, Float(pixels[i + 1]) / 255, Float(pixels[i + 2]) / 255)
            worst = min(worst, (dimmest + 0.05) / (background + 0.05))
            shown.append(background)
        }
        let spread = (shown.max() ?? 0) - (shown.min() ?? 0)
        XCTAssertGreaterThanOrEqual(worst, 4.5, "the dimmest text colour reads at every point a card can reach")
        XCTAssertGreaterThan(spread, 0.005, "cards are tinted locally, so the background still shows through them")
    }

    func testStatusColoursAreTheMacsTokens() {
        XCTAssertEqual(PaletteTokens.attention.dark, 0xFFB224)
        XCTAssertEqual(PhonePalette.textRoles(dark: true).count, PaletteTokens.textRoles.count)
    }
}

private final class UIConfigState: @unchecked Sendable {
    private let lock = NSLock()
    private var _query: [String: String] = [:]
    private var _body = ""
    var query: [String: String] { lock.withLock { _query } }
    var body: String {
        get { lock.withLock { _body } }
        set { lock.withLock { _body = newValue } }
    }
    func record(_ query: [String: String]) { lock.withLock { _query = query } }
    func reset() { lock.withLock { _query = [:]; _body = "" } }
}

private final class UIConfigURLProtocol: URLProtocol, @unchecked Sendable {
    static let state = UIConfigState()
    override class func canInit(with request: URLRequest) -> Bool { request.url?.path == "/api/v1/ui" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let components = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)!
        Self.state.record(Dictionary(uniqueKeysWithValues: (components.queryItems ?? []).map { ($0.name, $0.value ?? "") }))
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(Self.state.body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}
