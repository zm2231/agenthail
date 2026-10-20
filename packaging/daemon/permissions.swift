// agenthail-permissions reports, as one JSON object, the privacy grants macOS
// holds for the process responsible for it: the Agenthail Daemon bundle when
// the daemon runs it. Each argument is the bundle id of an app to report
// Automation for. It only reads the grants, so no prompt shows, unless the
// first argument is --ask: then macOS asks the person for each grant not yet
// decided, which also lists the bundle in System Settings.
import ApplicationServices
import Carbon
import CoreGraphics
import Foundation

var arguments = Array(CommandLine.arguments.dropFirst())
let ask = arguments.first == "--ask"
if ask { arguments.removeFirst() }

func automation(_ bundle: String) -> String {
    var target = AEAddressDesc()
    let created = bundle.withCString { AECreateDesc(DescType(typeApplicationBundleID), $0, bundle.utf8.count, &target) }
    guard created == noErr else { return "unknown" }
    defer { AEDisposeDesc(&target) }
    switch AEDeterminePermissionToAutomateTarget(&target, AEEventClass(typeWildCard), AEEventID(typeWildCard), ask) {
    case noErr: return "granted"
    case OSStatus(errAEEventNotPermitted): return "denied"
    case OSStatus(errAEEventWouldRequireUserConsent): return "missing"
    case OSStatus(procNotFound): return "target_not_running"
    default: return "unknown"
    }
}

let accessibility = ask
    ? AXIsProcessTrustedWithOptions([kAXTrustedCheckOptionPrompt.takeUnretainedValue(): true] as CFDictionary)
    : AXIsProcessTrusted()
let screenRecording = ask ? CGRequestScreenCaptureAccess() : CGPreflightScreenCaptureAccess()
let report: [String: Any] = [
    "accessibility": accessibility ? "granted" : "missing",
    "screenRecording": screenRecording ? "granted" : "missing",
    "automation": Dictionary(uniqueKeysWithValues: arguments.map { ($0, automation($0)) }),
]
let data = try JSONSerialization.data(withJSONObject: report, options: [.sortedKeys])
FileHandle.standardOutput.write(data)
FileHandle.standardOutput.write(Data("\n".utf8))
