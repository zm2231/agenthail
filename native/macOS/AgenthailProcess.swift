import Foundation

enum AgenthailProcess {
    static func run(_ arguments: [String]) {
        let process = configuredProcess(arguments)
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        try? process.run()
    }

    static func output(_ arguments: [String]) -> (Int32, String) {
        let process = configuredProcess(arguments)
        let pipe = Pipe()
        process.standardOutput = pipe
        process.standardError = pipe
        do {
            try process.run()
            process.waitUntilExit()
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            return (process.terminationStatus, String(decoding: data, as: UTF8.self))
        } catch {
            return (1, error.localizedDescription)
        }
    }

    private static func configuredProcess(_ arguments: [String]) -> Process {
        let process = Process()
        process.executableURL = executableURL()
        process.arguments = arguments
        var environment = ProcessInfo.processInfo.environment
        let root = "/Library/Application Support/Agenthail"
        if FileManager.default.fileExists(atPath: "\(root)/agenthail") {
            environment["AGENTHAIL_SIDECAR"] = "\(root)/sidecar.py"
            environment["AGENTHAIL_COOKIE_BRIDGE"] = "\(root)/cookie.mjs"
            environment["AGENTHAIL_PYTHON"] = "\(root)/runtime/python/bin/python3"
            environment["AGENTHAIL_MAC_APP"] = Bundle.main.executableURL?.path
            environment["PYTHONPATH"] = "\(root)/pydeps" + environmentSuffix(environment["PYTHONPATH"])
            environment["PATH"] = "\(root)/runtime/node/bin:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin" + environmentSuffix(environment["PATH"])
            environment["PYTHONDONTWRITEBYTECODE"] = "1"
        }
        process.environment = environment
        return process
    }

    private static func environmentSuffix(_ value: String?) -> String {
        guard let value, !value.isEmpty else { return "" }
        return ":\(value)"
    }

    private static func executableURL() -> URL {
        let environment = ProcessInfo.processInfo.environment
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let candidates = [
            environment["AGENTHAIL_CLI"],
            Bundle.main.resourceURL?.appendingPathComponent("agenthail").path,
            "/opt/homebrew/bin/agenthail",
            "/usr/local/bin/agenthail",
            "\(home)/.local/bin/agenthail"
        ].compactMap { $0 }.filter { !$0.isEmpty }
        if let path = candidates.first(where: { FileManager.default.isExecutableFile(atPath: $0) }) {
            return URL(fileURLWithPath: path)
        }
        return URL(fileURLWithPath: "/usr/bin/false")
    }
}
