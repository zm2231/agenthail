@testable import AgenthailHelpers
import Foundation

@main
struct AgenthailBuildTest {
    static func main() {
        func version(_ json: String) -> APIVersion {
            try! JSONDecoder().decode(APIVersion.self, from: Data(json.utf8))
        }
        let daemon = version(#"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1,"version":"v0.2.68","revision":"e387b0d87b52f557","modified":false,"builtAt":"2026-10-23T11:33:26Z"}"#).build
        expect(daemon == AgenthailBuild(version: "v0.2.68", revision: "e387b0d87b52f557"), "the daemon names its build")
        expect(daemon?.label == "v0.2.68 (e387b0d8)", "a build reads as doctor names it")
        expect(version(#"{"protocol":1,"minimumProtocol":1,"maximumProtocol":1}"#).build == nil, "an older daemon names no build")

        let app = AgenthailBuild.app(["CFBundleShortVersionString": "0.2.69", "AgenthailRevision": "abcd1234abcd1234", "AgenthailRevisionModified": false])
        expect(app == AgenthailBuild(version: "0.2.69", revision: "abcd1234abcd1234"), "the app reads its build from Info.plist")
        expect(AgenthailBuild.app(["CFBundleShortVersionString": "0.2.69"]) == nil, "an app built without its commit names no build")

        expect(AgenthailBuild.daemonNotice(app: app, daemon: daemon) == "The running daemon is v0.2.68 (e387b0d8); this app is 0.2.69 (abcd1234). Restart Agenthail to use this version.", "another build is named with both builds")
        expect(AgenthailBuild.daemonNotice(app: app, daemon: AgenthailBuild(version: "v0.2.69", revision: "abcd1234abcd1234")) == nil, "the same commit is quiet")
        expect(AgenthailBuild.daemonNotice(app: app, daemon: nil) == nil && AgenthailBuild.daemonNotice(app: nil, daemon: daemon) == nil, "an unnamed build is not compared")
        expect(AgenthailBuild.daemonNotice(app: app, daemon: AgenthailBuild(version: "dev", revision: "unknown")) == nil, "an unknown revision is not compared")
        expect(AgenthailBuild.daemonNotice(app: app, daemon: AgenthailBuild(version: "dev", revision: "e387b0d87b52f557", modified: true)) == nil, "a modified tree is not compared")

        let peer = try! JSONDecoder().decode(HostHealth.self, from: Data(#"{"id":"h1","name":"server","state":"connected","build":{"version":"v0.2.68","revision":"e387b0d87b52f557","modified":false}}"#.utf8))
        expect(peer.build?.label == "v0.2.68 (e387b0d8)", "a paired host names its build")
        print("agenthail build tests passed")
    }

    static func expect(_ condition: Bool, _ message: String) {
        if !condition {
            FileHandle.standardError.write(Data("FAIL: \(message)\n".utf8))
            exit(1)
        }
    }
}
