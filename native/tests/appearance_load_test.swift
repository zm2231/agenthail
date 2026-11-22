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

    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "appearance.test" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func stopLoading() {}

    override func startLoading() {
        let url = request.url!
        guard url.path == "/api/v1/ui/background" else { return send(Data(Self.lock.withLock { Self.body }.utf8), type: "application/json") }
        let path = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "path" }?.value ?? ""
        let (image, delay) = Self.lock.withLock { (Self.images[path] ?? Data(), Self.delays[path] ?? 0) }
        let request = self
        DispatchQueue.global().asyncAfter(deadline: .now() + delay) { request.send(image, type: "image/png") }
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
            AppearanceDaemon.images = ["art/slow-dark.jpg": image, "art/slow-light.jpg": image, "art/sea.jpg": image, "art/dune.jpg": image, "art/old.jpg": image, "art/new.jpg": image]
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
        check(switching.config.rev == "two" && switching.wallpapers["art/dune.jpg"]?.id == "art/sea.jpg", "the light side keeps the last image while its own loads")
        await wait { switching.wallpapers["art/dune.jpg"]?.id == "art/dune.jpg" }
        check(switching.wallpapers["art/dune.jpg"]?.id == "art/dune.jpg", "the light image shows once it lands")

        let stale = InterfaceStore()
        serve(#"{"rev":"one","values":{"theme":"dark","background":"image: art/old.jpg"},"chosen":{},"overrides":{},"problems":[]}"#, delays: ["art/old.jpg": 1.5])
        stale.load(from: api)
        await wait { stale.config.rev == "one" }
        serve(#"{"rev":"two","values":{"theme":"dark","background":"image: art/new.jpg"},"chosen":{},"overrides":{},"problems":[]}"#)
        stale.load(from: api)
        await wait { stale.wallpapers["art/new.jpg"]?.id == "art/new.jpg" }
        try? await Task.sleep(for: .seconds(2))
        check(stale.config.rev == "two" && Array(stale.wallpapers.keys) == ["art/new.jpg"], "an older load's late image never lands: \(stale.wallpapers.keys)")
        print("appearance load: ok")
    }
}
