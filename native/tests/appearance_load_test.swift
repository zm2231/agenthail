@testable import AgenthailHelpers
import AppKit
import Foundation
import ImageIO
import UniformTypeIdentifiers

final class AppearanceDaemon: URLProtocol, @unchecked Sendable {
    static let lock = NSLock()
    nonisolated(unsafe) static var body = ""
    nonisolated(unsafe) static var images: [String: Data] = [:]
    nonisolated(unsafe) static var delays: [String: Double] = [:]
    nonisolated(unsafe) static var answers: [String: String] = [:]

    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "appearance.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        if url.path.hasPrefix("/api/v1/ui/snapshot/") {
            let body = Self.body(of: request)
            let error = (try? JSONSerialization.jsonObject(with: body) as? [String: Any])?["error"] as? String
            Self.lock.withLock { Self.answers[url.lastPathComponent] = error ?? "png" }
            return send(Data("{}".utf8), type: "application/json")
        }
        guard url.path == "/api/v1/ui/background" else { return send(Data(Self.lock.withLock { Self.body }.utf8), type: "application/json") }
        let path = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "path" }?.value ?? ""
        let (image, delay) = Self.lock.withLock { (Self.images[path] ?? Data(), Self.delays[path] ?? 0) }
        let request = self
        DispatchQueue.global().asyncAfter(deadline: .now() + delay) { request.send(image, type: "image/png") }
    }

    static func body(of request: URLRequest) -> Data {
        if let data = request.httpBody { return data }
        guard let stream = request.httpBodyStream else { return Data() }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while true {
            let count = stream.read(&buffer, maxLength: buffer.count)
            if count <= 0 { break }
            data.append(buffer, count: count)
        }
        return data
    }

    private func send(_ data: Data, type: String) {
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": type])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
    }
}

@main
struct AppearanceLoadTest {
    static func check(_ condition: Bool, _ message: String) {
        guard !condition else { return }
        print("FAIL: \(message)")
        exit(1)
    }

    static func serve(_ body: String, delays: [String: Double] = [:]) {
        AppearanceDaemon.lock.withLock {
            AppearanceDaemon.body = body
            AppearanceDaemon.delays = delays
        }
    }

    @MainActor
    static func wait(_ seconds: Double = 5, until done: () -> Bool) async {
        let deadline = Date().addingTimeInterval(seconds)
        while !done(), Date() < deadline { try? await Task.sleep(for: .milliseconds(5)) }
    }

    static func png() -> Data {
        let context = CGContext(data: nil, width: 8, height: 8, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
        context.setFillColor(CGColor(red: 0.2, green: 0.6, blue: 0.7, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: 8, height: 8))
        let data = NSMutableData()
        let destination = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil)!
        CGImageDestinationAddImage(destination, context.makeImage()!, nil)
        CGImageDestinationFinalize(destination)
        return data as Data
    }

    @MainActor
    static func main() async {
        _ = NSApplication.shared
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [AppearanceDaemon.self]
        let api = AgenthailAPI(baseURL: URL(string: "http://appearance.test")!, token: "t", session: URLSession(configuration: configuration))
        let image = png()
        AppearanceDaemon.lock.withLock {
            AppearanceDaemon.images = ["art/slow-dark.jpg": image, "art/slow-light.jpg": image, "art/sea.jpg": image, "art/dune.jpg": image, "art/old.jpg": image, "art/new.jpg": image,
                                       "art/night.jpg": image, "art/day.jpg": image, "art/both.jpg": Data("not an image".utf8), "art/slow.jpg": image, "art/bad.jpg": Data("not an image".utf8),
                                       "art/held.jpg": Data("not an image".utf8), "art/first.jpg": image, "art/ready.jpg": image]
        }
        serve(#"{"rev":"two","values":{"theme":"dark","glass.opacity":0.45,"background":"image: art/slow-dark.jpg","background.light":"image: art/slow-light.jpg"},"chosen":{},"overrides":{},"problems":[]}"#, delays: ["art/slow-dark.jpg": 0.5, "art/slow-light.jpg": 2])
        let store = InterfaceStore()
        let start = Date()
        var configAt: Double?
        var darkAt: Double?
        var lightAt: Double?
        store.load(from: api)
        while Date().timeIntervalSince(start) < 6, configAt == nil || darkAt == nil || lightAt == nil {
            let now = Date().timeIntervalSince(start) * 1000
            if configAt == nil, store.config.rev == "two" { configAt = now }
            if darkAt == nil, store.wallpapers["art/slow-dark.jpg"] != nil { darkAt = now }
            if lightAt == nil, store.wallpapers["art/slow-light.jpg"] != nil { lightAt = now }
            try? await Task.sleep(for: .milliseconds(5))
        }
        print("appearance-timing config=\(Int(configAt ?? -1))ms active-dark=\(Int(darkAt ?? -1))ms inactive-light=\(Int(lightAt ?? -1))ms")
        check((configAt ?? .infinity) < 400, "the settings apply without waiting on any image")
        check((darkAt ?? .infinity) < 1500 && (darkAt ?? .infinity) < (lightAt ?? 0), "the active image shows first, once it lands")

        let switching = InterfaceStore()
        serve(#"{"rev":"one","values":{"theme":"dark","background":"image: art/sea.jpg"},"chosen":{},"overrides":{},"problems":[]}"#)
        switching.load(from: api)
        await wait { switching.config.rev == "one" && switching.wallpapers["art/sea.jpg"] != nil }
        serve(#"{"rev":"two","values":{"theme":"light","background":"image: art/sea.jpg","background.light":"image: art/dune.jpg"},"chosen":{},"overrides":{},"problems":[]}"#, delays: ["art/dune.jpg": 1])
        switching.load(from: api)
        await wait { switching.config.rev == "two" }
        check(switching.config.rev == "two" && switching.drawn[false]?.id == "art/sea.jpg", "the light side keeps the last image while its own loads")
        check(!switching.settled, "a snapshot waits while the light image loads")
        await wait { switching.drawn[false]?.id == "art/dune.jpg" }
        check(switching.drawn[false]?.id == "art/dune.jpg" && switching.settled, "the light image shows once it lands and the load settles")

        let shared = InterfaceStore()
        serve(#"{"rev":"one","values":{"background":"image: art/night.jpg","background.light":"image: art/day.jpg"},"chosen":{},"overrides":{},"problems":[]}"#)
        shared.load(from: api)
        await wait { shared.wallpapers.count == 2 }
        serve(#"{"rev":"two","values":{"background":"image: art/both.jpg"},"chosen":{},"overrides":{},"problems":[]}"#)
        shared.load(from: api)
        await wait { shared.problem != nil }
        check(shared.config.rev == "two" && shared.drawn[true]?.id == "art/night.jpg" && shared.drawn[false]?.id == "art/day.jpg", "each appearance keeps its own image when both move to one that fails: \(shared.drawn.mapValues(\.id))")

        let overlap = InterfaceStore()
        serve(#"{"rev":"one","values":{"theme":"dark","background":"image: art/slow.jpg"},"chosen":{},"overrides":{},"problems":[]}"#, delays: ["art/slow.jpg": 1.5])
        overlap.load(from: api)
        await wait { overlap.config.rev == "one" }
        serve(#"{"rev":"two","values":{"theme":"dark","background":"image: art/bad.jpg"},"chosen":{},"overrides":{},"problems":[]}"#)
        _ = await overlap.change(UIChange(action: "set", set: ["background = image: art/bad.jpg"], message: "bad"), api: api)
        check(overlap.problem == "The background image art/bad.jpg can't be read.", "the change reports its own failed image: \(overlap.problem ?? "none")")
        try? await Task.sleep(for: .seconds(2))
        check(overlap.wallpapers["art/slow.jpg"] == nil && overlap.problem == "The background image art/bad.jpg can't be read.", "the superseded load lands neither its image nor its problem")

        let stale = InterfaceStore()
        serve(#"{"rev":"one","values":{"theme":"dark","background":"image: art/old.jpg"},"chosen":{},"overrides":{},"problems":[]}"#, delays: ["art/old.jpg": 1.5])
        stale.load(from: api)
        await wait { stale.config.rev == "one" }
        serve(#"{"rev":"two","values":{"theme":"dark","background":"image: art/new.jpg"},"chosen":{},"overrides":{},"problems":[]}"#)
        stale.load(from: api)
        await wait { stale.wallpapers["art/new.jpg"]?.id == "art/new.jpg" }
        try? await Task.sleep(for: .seconds(2))
        check(stale.config.rev == "two" && Array(stale.wallpapers.keys) == ["art/new.jpg"], "an older load's late image never lands: \(stale.wallpapers.keys)")
        check(overlap.snapshotProblem(for: "two") != nil, "a snapshot of a revision whose image failed reports a problem instead of a fallback")

        let cancelled = InterfaceStore()
        serve(#"{"rev":"one","values":{"theme":"dark","background":"image: art/held.jpg"},"chosen":{},"overrides":{},"problems":[]}"#, delays: ["art/held.jpg": 1])
        let pending = Task { await cancelled.change(UIChange(action: "set", set: ["background = image: art/held.jpg"], message: "held"), api: api) }
        await wait { cancelled.config.rev == "one" }
        check(!cancelled.settled && cancelled.snapshotProblem(for: "one") != nil, "a snapshot waits while the image is held")
        pending.cancel()
        _ = await pending.value
        try? await Task.sleep(for: .seconds(1.5))
        check(cancelled.problem == nil && cancelled.wallpapers.isEmpty, "a cancelled change publishes neither a problem nor an image: \(cancelled.problem ?? "none")")
        check(cancelled.settled, "a cancelled change does not leave snapshots waiting")

        let racing = InterfaceStore()
        serve(#"{"rev":"one","values":{"theme":"dark","background":"image: art/first.jpg"},"chosen":{},"overrides":{},"problems":[]}"#, delays: ["art/first.jpg": 1])
        let earlier = Task { await racing.change(UIChange(action: "set", set: ["background = image: art/first.jpg"], message: "first"), api: api) }
        await wait { racing.config.rev == "one" }
        serve(#"{"rev":"two","values":{"theme":"dark","background":"image: art/bad.jpg"},"chosen":{},"overrides":{},"problems":[]}"#)
        _ = await racing.change(UIChange(action: "set", set: ["background = image: art/bad.jpg"], message: "second"), api: api)
        _ = await earlier.value
        try? await Task.sleep(for: .seconds(1.5))
        check(racing.problem == "The background image art/bad.jpg can't be read." && racing.wallpapers["art/first.jpg"] == nil, "only the newer change publishes: \(racing.problem ?? "none") \(racing.wallpapers.keys)")

        let ready = InterfaceStore()
        serve(#"{"rev":"one","values":{"theme":"dark","background":"image: art/ready.jpg"},"chosen":{},"overrides":{},"problems":[]}"#)
        ready.load(from: api)
        await wait { ready.config.rev == "one" && ready.settled && ready.wallpapers["art/ready.jpg"] != nil }
        check(ready.snapshotProblem(for: "one") == nil, "a snapshot proceeds once the image shows")
        check(ready.snapshotProblem(for: "two") != nil, "a snapshot of a revision that hasn't arrived reports it")

        let shown = InterfaceStore()
        serve(#"{"rev":"mesh","values":{"theme":"dark","background":"mesh: 2x2, #F0E0D0, #203040, #E0F0C0, #401020"},"chosen":{},"overrides":{},"problems":[]}"#)
        shown.load(from: api)
        await wait { shown.config.rev == "mesh" && shown.settled }
        let window = NSWindow(contentRect: CGRect(x: -20000, y: -20000, width: 96, height: 48), styleMask: [.borderless], backing: .buffered, defer: false)
        window.contentView = NSView(frame: CGRect(x: 0, y: 0, width: 96, height: 48))
        window.appearance = NSAppearance(named: .darkAqua)
        let backdrop = WindowBackdrop()
        backdrop.configure(UIConfig(), wallpapers: [:])
        backdrop.windowChanged(window)
        let budget = SnapshotBudget(deadlineMs: 2300)
        await budget.wait { backdrop.shows(shown) }
        check(!backdrop.shows(shown), "a snapshot whose backdrop never draws the store's config is still not drawn when its budget runs out")
        backdrop.configure(shown.config, wallpapers: shown.drawn)
        check(!backdrop.shows(shown), "a backdrop given the config shows it only once rendered")
        await wait(30) { backdrop.shows(shown) }
        check(backdrop.shows(shown), "the backdrop shows the store's config once rendered")
        print("appearance load: ok")
    }
}
