import Foundation

let identifier = Bundle.main.bundleIdentifier
let marker = identifier == "com.agenthail.app" ? "legacy-fixture-started" : "unrelated-fixture-started"
let directory = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".agenthail", isDirectory: true)
try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
FileManager.default.createFile(atPath: directory.appendingPathComponent(marker).path, contents: Data())
RunLoop.current.run()
