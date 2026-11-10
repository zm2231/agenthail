#if DEBUG
import SwiftUI

// Public demo data exercises the real session views without pairing or network access.
struct SessionPreview: View {
    @State private var model: AgenthailIOSModel
    private let session: SessionState
    private let detail: SessionDetail

    init() {
        let decoder = JSONDecoder()
        let capture = SessionCapture.current
        let snapshotJSON = Self.withTurnNotice(capture?.snapshotJSON ?? Self.snapshotJSON)
        let snapshot = try! decoder.decode(DashboardSnapshot.self, from: Data(snapshotJSON.utf8))
        let view = (try? decoder.decode(ScopePage.self, from: Data(PreviewOrder.withView(snapshotJSON).utf8)))?.view
        let capturedDetail = capture.flatMap { $0.detailJSON[$0.initialSessionID] }
        var detail = try! decoder.decode(SessionDetail.self, from: Data((capturedDetail ?? Self.previewDetailJSON).utf8))
        if ProcessInfo.processInfo.arguments.contains("--preview-app"),
           let metadata = try? decoder.decode(SessionMetadata.self, from: Data(Self.previewMetadataJSON.utf8)) {
            detail.claudeRuns = metadata.claudeRuns
            detail.claudeSubagents = metadata.claudeSubagents
        }
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [SessionPreviewProtocol.self]
        let model = AgenthailIOSModel(
            api: AgenthailAPI(baseURL: URL(string: "https://preview.invalid")!, token: "public-demo", session: URLSession(configuration: config)),
            paired: ProcessInfo.processInfo.arguments.contains("--preview-voice-entry"),
            hostName: "studio-mac"
        )
        model.setHostSnapshot(snapshot, view: view)
        detail.session.id = HostCatalog.qualify("computer", detail.session.id)
        if ProcessInfo.processInfo.arguments.contains("--preview-named") { detail.alias = "builder" }
        self.detail = detail
        session = model.snapshot?.sessions.first(where: { $0.id == detail.session.id }) ?? detail.sessionState(open: false)
        model.selectedSessionID = session.id
        model.selectedDetail = detail
        if capture == nil { model.composer = "Keep the regression check with the fix." }
        if ProcessInfo.processInfo.arguments.contains("--preview-delivery") {
            model.deliveryStatus[session.id] = "Queued for the agent"
        }
        _model = State(initialValue: model)
    }
    var body: some View {
        AppearanceScope(appearance: model.appearance) { previewBody }
            .onAppear {
                guard let config = Self.previewAppearance else { return }
                model.appearance.preview(config, wallpapers: Self.previewWallpapers(config))
            }
            .task { await PreviewBackgroundUpserts.run(model, sparing: session.id) }
    }

    /// An `image:` backdrop is read from the path in the argument, which the
    /// harness places inside the app's own container.
    static func previewWallpapers(_ config: UIConfig) -> [WallpaperImage] {
        let screen = UIScreen.main.nativeBounds.size
        return config.imagePaths.compactMap { path in
            (try? Data(contentsOf: URL(fileURLWithPath: path))).flatMap { WallpaperImage.decode($0, id: path, maxPixels: Int(max(screen.width, screen.height))) }
        }
    }

    static var previewAppearance: UIConfig? {
        let arguments = ProcessInfo.processInfo.arguments
        let theme = arguments.firstIndex(of: "--preview-theme").flatMap { $0 + 1 < arguments.count ? arguments[$0 + 1] : nil }
        var values: [String: Any] = ["theme": theme ?? "system"]
        if let index = arguments.firstIndex(of: "--preview-backdrop") {
            values["background"] = index + 1 < arguments.count && !arguments[index + 1].hasPrefix("--") ? arguments[index + 1] : "mesh: 3x3, #2e1972, #182c58, #0e5671, #2d1655, #386870, #2e3340, #591948, #8a3049, #682e2c"
        } else if theme == nil {
            return nil
        }
        for (flag, setting) in zip(arguments, arguments.dropFirst()) where flag == "--preview-value" {
            let parts = setting.split(separator: "=", maxSplits: 1).map(String.init)
            guard parts.count == 2 else { continue }
            values[parts[0]] = Double(parts[1]) ?? parts[1]
        }
        guard let json = try? JSONSerialization.data(withJSONObject: ["rev": "", "problems": [], "chosen": [:], "overrides": [:], "values": values]) else { return nil }
        return try? JSONDecoder().decode(UIConfig.self, from: json)
    }

    @ViewBuilder private var previewBody: some View {
        if ProcessInfo.processInfo.arguments.contains("--preview-app") {
            MainTabs(model: model)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-new") {
            NewSessionSheet(model: model)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-inspector") {
            SessionInspector(model: model, session: session, initialDetail: detail)
        } else if ProcessInfo.processInfo.arguments.contains("--preview-project") {
            ConversationFlow(model: model, initialSessionID: ConversationRoute.project(HostCatalog.qualify("computer", ProjectNowPreview.scenario == nil ? "project-fieldnotes" : ProjectNowPreview.projectID)))
        } else if ProcessInfo.processInfo.arguments.contains("--preview-workspace") {
            ConversationFlow(model: model, initialSessionID: session.id)
        } else {
            NavigationStack { SessionScreen(model: model, session: session) }
        }
    }
    /// With --preview-turn-notice the first session is a terminal session that reads messages as notes.
    static func withTurnNotice(_ json: String) -> String {
        guard ProcessInfo.processInfo.arguments.contains("--preview-turn-notice") else { return json }
        if !ProcessInfo.processInfo.arguments.contains("--preview-keep-defaults") {
            UserDefaults.standard.removeObject(forKey: TurnNoticePreference.hiddenKey)
            UserDefaults.standard.removeObject(forKey: TurnNoticePreference.dismissedKey)
        }
        var snapshot = try! JSONSerialization.jsonObject(with: Data(json.utf8)) as! [String: Any]
        var sessions = snapshot["sessions"] as! [[String: Any]]
        var capabilities = sessions[0]["capabilities"] as? [String: Any] ?? [:]
        capabilities["personMessage"] = "note"
        capabilities["remoteControl"] = false
        sessions[0]["capabilities"] = capabilities
        snapshot["sessions"] = sessions
        return String(data: try! JSONSerialization.data(withJSONObject: snapshot), encoding: .utf8)!
    }

    /// The question a waiting preview session asks in its terminal.
    nonisolated static let previewWaiting: [String: String] = ["kind": "question", "id": "preview-question"]

    /// With --preview-unreachable the session runs in the host and waits on an
    /// approval Agenthail does not hold, and another session is stopped by a
    /// usage limit.
    nonisolated static var previewUnreachable: [String: Any] { ["kind": "approval", "id": "preview-approval", "answerable": false] }
    nonisolated static var previewQuestions: [String: Any] { ["kind": "question", "id": "preview-questions", "answerable": true] }
    nonisolated static let previewQuestionsJSON = #"{"permissionMode":"","requests":[{"id":"preview-questions","kind":"question","tool":"AskUserQuestion","alwaysAllow":false,"createdAt":"2026-09-12T04:03:00Z","questions":[{"question":"The reconnect fix touches the session store and the stream reader. Which part should land first so the other can build on it?","header":"Landing order for the reconnect fix","multiSelect":false,"options":[{"id":"q0o0","label":"Session store first","description":"Keeps the saved identity stable before the reader changes"},{"id":"q0o1","label":"Stream reader first","description":"Smaller change that unblocks the failing regression test today"},{"id":"q0o2","label":"Both together","description":"One pull request, one review, one risk window"},{"id":"q0o3","label":"Neither yet","description":"Write the failing test first and decide after"}]},{"question":"Where should the regression check run?","header":"Checks","multiSelect":true,"options":[{"id":"q1o0","label":"Mac","description":"Run the suite on the desktop build"},{"id":"q1o1","label":"iPhone","description":"Run it on the shared simulator"},{"id":"q1o2","label":"Dashboard","description":"Run it in the browser harness"}]}]}]}"#
    nonisolated static let previewHostedLifecycle: [String: Bool] = ["status": true, "logs": true, "stop": true, "resume": true]

    nonisolated static var previewTerminalQuestion: [String: Any] { ["kind": "question", "id": "demo:question:held:preview-1", "answerable": true] }
    nonisolated static let previewTerminalQuestionInput = #"{"questions":[{"question":"Which build should the regression check run against?","header":"Build","multiSelect":false,"options":[{"id":"o0","label":"Debug","description":"Fast to build, matches local runs"},{"id":"o1","label":"Release","description":"Matches what ships"}]}]}"#
    nonisolated static var previewTerminalQuestionJSON: String {
        var request = try! JSONSerialization.jsonObject(with: Data(previewTerminalQuestionInput.utf8)) as! [String: Any]
        request.merge(["id": "demo:question:held:preview-1", "kind": "question", "tool": "AskUserQuestion", "alwaysAllow": false, "createdAt": "2026-09-12T04:03:00Z"]) { $1 }
        return String(data: try! JSONSerialization.data(withJSONObject: ["permissionMode": "", "requests": [request]]), encoding: .utf8)!
    }
    nonisolated static func terminalQuestion(_ session: inout [String: Any], capabilities: inout [String: Any]) {
        session["waitingOn"] = previewTerminalQuestion
        session["runtimeKind"] = "claude-cli"
        session["runtime"] = ["launcher": "external", "focusable": false]
        capabilities["respond"] = true
        capabilities["lifecycle"] = ["status": false, "logs": false, "stop": false, "resume": false, "adopt": true]
    }
    nonisolated static let previewUsageLimit: [String: String] = ["resetsAt": "2026-09-12T18:20:00Z"]

    nonisolated static let terminalAttention = ["kind": "request", "reason": "Waiting in its terminal"]
    nonisolated static let questionsAttention = ["kind": "request", "reason": "Answer in Agenthail"]
    nonisolated static let unreachableAttention = ["kind": "request", "reason": "Waiting on a prompt Agenthail can't reach. Stop and resume to clear it"]

    nonisolated static var previewDetailJSON: String { previewDetail(arguments: ProcessInfo.processInfo.arguments) }

    nonisolated static func previewDetail(arguments: [String]) -> String {
        guard arguments.contains("--preview-unreachable") || arguments.contains("--preview-questions") || arguments.contains("--preview-limit-move") || arguments.contains("--preview-terminal-question") || SessionOperationsPreview.enabled else { return basePreviewDetailJSON }
        var detail = try! JSONSerialization.jsonObject(with: Data(basePreviewDetailJSON.utf8)) as! [String: Any]
        if SessionOperationsPreview.enabled { SessionOperationsPreview.apply(detail: &detail) }
        if arguments.contains("--preview-terminal-question") {
            var session = detail["session"] as! [String: Any]
            var capabilities = detail["capabilities"] as! [String: Any]
            terminalQuestion(&session, capabilities: &capabilities)
            detail["session"] = session
            detail["capabilities"] = capabilities
            detail["attention"] = terminalAttention
            var timeline = detail["timeline"] as! [String: Any]
            var items = timeline["items"] as! [[String: Any]]
            items.append(["id": "ask-1", "kind": "toolCall", "title": "AskUserQuestion", "text": previewTerminalQuestionInput, "callId": "ask-1", "timestamp": "2026-09-12T04:03:00Z", "truncated": false])
            timeline["items"] = items
            detail["timeline"] = timeline
        }
        if arguments.contains("--preview-limit-move") {
            detail["limitResume"] = ["state": "waiting", "resetsAt": "2026-09-12T18:20:00Z", "moveTo": ["root": "team", "account": "team@example.com", "reason": "most headroom, session 18%, week 40%"]]
        }
        if arguments.contains("--preview-questions") {
            var session = detail["session"] as! [String: Any]
            session["waitingOn"] = previewQuestions
            detail["session"] = session
            detail["attention"] = questionsAttention
            var capabilities = detail["capabilities"] as! [String: Any]
            capabilities["respond"] = true
            detail["capabilities"] = capabilities
        }
        if arguments.contains("--preview-unreachable") {
            var session = detail["session"] as! [String: Any]
            session["waitingOn"] = previewUnreachable
            session["runtimeKind"] = "claude-host"
            detail["session"] = session
            detail["attention"] = unreachableAttention
            var capabilities = detail["capabilities"] as! [String: Any]
            capabilities["respond"] = true
            capabilities["lifecycle"] = previewHostedLifecycle
            detail["capabilities"] = capabilities
        }
        return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
    }

    nonisolated static var basePreviewDetailJSON: String {
        let reading = ProcessInfo.processInfo.arguments.contains("--preview-reading")
        let longActivity = ProcessInfo.processInfo.arguments.contains("--preview-long-activity")
        guard ProcessInfo.processInfo.arguments.contains("--preview-app") || reading || longActivity else { return detailJSON }
        var detail = try! JSONSerialization.jsonObject(with: Data(detailJSON.utf8)) as! [String: Any]
        var timeline = detail["timeline"] as! [String: Any]
        var items = timeline["items"] as! [[String: Any]]
        if longActivity {
            // One run of many commands with recorded output, between two replies.
            let runs: [[String: Any]] = (1...30).flatMap { n -> [[String: Any]] in
                let output = (1...30).map { "ok \(n).\($0) fieldnotes/Sources/Reconnect\($0).swift compiled" }.joined(separator: "\n")
                return [
                    ["id": "run-\(n)", "kind": "toolCall", "title": "Bash", "text": "{\"command\":\"swift build --target Reconnect\(n)\"}", "callId": "run-\(n)", "truncated": false],
                    ["id": "run-\(n)-result", "kind": "toolResult", "title": "Bash result", "text": output, "callId": "run-\(n)", "truncated": false],
                ]
            }
            items.insert(contentsOf: runs, at: 4)
            items.append(["id": "after-run", "kind": "message", "role": "assistant", "title": "assistant", "text": "Every target builds after reconnecting.", "truncated": false])
            if ProcessInfo.processInfo.arguments.contains("--preview-long-history") { items.insert(contentsOf: Self.longHistory, at: 0) }
            detail["timeline"] = timeline.merging(["items": items, "nextBefore": 0]) { $1 }
            return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
        }
        items.removeLast()
        items.append(["id": "5", "kind": "reasoning", "title": "Thinking", "text": "The regression test covers the reconnect boundary. The remaining check is whether the queued instruction retains its session identity.", "truncated": false])
        items.append(["id": "6", "kind": "message", "role": "assistant", "title": "assistant", "text": "## Reconnect is fixed\n\nThe pending build survives reconnecting. **All 8 regression tests pass.**\n\n- Preserved the session identity\n- Kept the queued instruction attached\n- Added coverage for interrupted delivery\n\n```swift\nawait session.restorePendingBuild()\n```\n\n| Check | Result |\n| --- | --- |\n| Reconnect | Passed |\n| Queue identity | Passed |", "timestamp": "2026-09-12T04:03:00Z", "truncated": false])
        items.append(["id": "7", "kind": "event", "title": "Turn duration", "text": "1m55s", "timestamp": "2026-09-12T04:03:00Z", "truncated": false])
        if reading {
            let instructions = "# AGENTS.md instructions for /Users/demo/projects/fieldnotes\n\n" + String(repeating: "## Repository guidance\n\nRead the current implementation before editing. Preserve the session identity and include meaningful regression coverage.\n\n", count: 30)
            items.insert(["id": "context", "kind": "instructions", "title": "Instructions", "text": instructions, "truncated": false], at: 0)
            items.insert(["id": "long-message", "kind": "message", "role": "user", "title": "user", "text": String(repeating: "The phone should show the actual work clearly, including commands, results and context. Keep the transcript readable while the agent works.\n\n", count: 14), "truncated": false], at: 1)
            let edit = ["file_path": "/Users/demo/projects/fieldnotes/Sources/Reconnect.swift", "old_string": "let session = restore()\nawait session.resume()", "new_string": "let session = restore(keeping: identity)\nawait session.resume()\nawait session.restorePendingBuild()"]
            items.insert(contentsOf: [
                ["id": "edit-1", "kind": "toolCall", "title": "Edit", "text": String(data: try! JSONSerialization.data(withJSONObject: edit, options: .sortedKeys), encoding: .utf8)!, "callId": "edit-1", "truncated": false],
                ["id": "edit-1-result", "kind": "toolResult", "title": "Edit result", "text": "The file has been updated.", "callId": "edit-1", "truncated": false],
            ], at: items.count - 3)
            items[items.count - 2]["text"] = "# The session stays readable while work continues\n\nI found the reconnect boundary. The saved instruction is present, and the agent has its original session identity.\n\n## What the checks show\n\nThe regression suite passes. You can expand the recorded command above to inspect its full input and result.\n\nThe latest update is visible above the composer."
        }
        timeline["items"] = items
        timeline["nextBefore"] = 0
        detail["timeline"] = timeline
        return String(data: try! JSONSerialization.data(withJSONObject: detail), encoding: .utf8)!
    }

    nonisolated static var snapshotJSON: String {
        let capabilities: [String: Bool] = ["send": true, "stream": true, "reply": true, "goal": true, "goalSet": true, "goalStatus": true, "compact": true, "model": true, "interrupt": true, "steer": true, "steerImages": true, "attach": true]
        let sessions: [[String: Any]] = [
            ["id": "demo", "project": ["id": "project-fieldnotes", "displayName": "fieldnotes", "rootPath": "/Users/demo/projects/fieldnotes", "kind": "git"], "name": "Make the build reliable", "surface": "claude", "cwd": "/Users/demo/projects/fieldnotes", "status": "busy", "lastActive": "2026-09-12T04:03:00Z", "queueCount": 1, "open": true, "current": true, "capabilities": capabilities, "plan": ["done": 3, "total": 5, "current": "Running the regression suite"]],
            ["id": "codex-demo", "project": ["id": "project-fieldnotes", "displayName": "fieldnotes", "rootPath": "/Users/demo/projects/fieldnotes", "kind": "git"], "name": "Review the release pipeline", "surface": "codex", "cwd": "/Users/demo/projects/fieldnotes", "status": "idle", "lastActive": "2026-09-12T03:45:00Z", "queueCount": 0, "open": true, "current": true, "capabilities": capabilities],
            ["id": "saved-demo", "project": ["id": "project-agenthail", "displayName": "agenthail", "rootPath": "/Users/demo/projects/agenthail", "kind": "git"], "name": "Map the application architecture", "surface": "claude", "cwd": "/Users/demo/projects/agenthail", "status": "idle", "lastActive": "2026-09-11T13:00:00Z", "queueCount": 0, "open": false, "current": false, "capabilities": capabilities, "plan": ["done": 2, "total": 4]],
            ["id": "h0123456789ab::remote-demo", "project": ["id": "h0123456789ab::project-agenthail", "displayName": "agenthail", "rootPath": "/home/demo/work/agenthail", "kind": "git"], "host": ["id": "h0123456789ab", "name": "vps.example.ts.net", "state": "connected"], "name": "Prove the history is true", "surface": "codex", "cwd": "/home/demo/work/agenthail/.worktrees/history-truth", "status": "busy", "lastActive": "2026-09-12T04:01:00Z", "queueCount": 0, "open": true, "current": true, "capabilities": capabilities],
            ["id": "h0123456789ab::remote-demo-2", "project": ["id": "h0123456789ab::project-agenthail", "displayName": "agenthail", "rootPath": "/home/demo/work/agenthail", "kind": "git"], "host": ["id": "h0123456789ab", "name": "vps.example.ts.net", "state": "connected"], "name": "Tighten the release notes", "surface": "claude", "cwd": "/home/demo/work/agenthail/.worktrees/release-notes", "status": "idle", "lastActive": "2026-09-12T03:30:00Z", "queueCount": 0, "open": true, "current": true, "capabilities": capabilities, "claudeConfigRoot": ["name": "work", "root": "/home/dev/.work/.claude"]]
        ]
        let projects: [[String: Any]] = [["id": "project-fieldnotes", "displayName": "fieldnotes", "checkouts": [["host": ["id": "computer", "name": "computer", "state": "local"], "projectId": "project-fieldnotes", "rootPath": "/Users/demo/projects/fieldnotes", "added": true]], "coordinators": [["name": "release-lead", "surface": "codex", "sessionId": "codex-demo"]]]]
        var snapshot: [String: Any] = ["updatedAt": "2026-09-12T04:03:00Z", "daemon": ["running": true], "surfaces": [], "sessions": sessions, "totalSessions": sessions.count, "queue": previewSnapshotQueue,"channels": [], "relays": [], "history": [], "codexRecentHours": 24, "projects": projects]
        if ProcessInfo.processInfo.arguments.contains("--preview-checkouts") {
            var checked = snapshot["sessions"] as! [[String: Any]]
            checked[0]["checkout"] = ["id": "checkout-main", "path": "/Users/demo/projects/fieldnotes", "branch": "main", "isMain": true, "dirty": false]
            checked[1]["checkout"] = ["id": "checkout-fix", "path": "/Users/demo/projects/fieldnotes/.worktrees/fix-login", "branch": "fix-login", "isMain": false, "dirty": true]
            checked[2]["checkout"] = ["id": "checkout-agenthail", "path": "/Users/demo/projects/agenthail", "branch": "main", "isMain": true, "dirty": true]
            snapshot["sessions"] = checked
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-two-computers") {
            let vps: [String: Any] = ["id": "h0123456789ab", "name": "vps.example.ts.net", "state": "connected"]
            snapshot["daemon"] = ["running": true, "hosts": [vps]]
            var spanning = projects
            spanning[0]["checkouts"] = (projects[0]["checkouts"] as! [[String: Any]]) + [["host": vps, "projectId": "h0123456789ab::project-fieldnotes", "rootPath": "/home/demo/work/fieldnotes", "added": true]]
            spanning[0]["coordinators"] = (projects[0]["coordinators"] as! [[String: Any]]) + [["host": vps, "name": "server-lead", "surface": "claude", "sessionId": "remote-demo"]]
            snapshot["projects"] = spanning
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-questions") {
            var asking = snapshot["sessions"] as! [[String: Any]]
            asking[0]["waitingOn"] = previewQuestions
            asking[0]["attention"] = questionsAttention
            var askingCapabilities: [String: Any] = capabilities
            askingCapabilities["respond"] = true
            asking[0]["capabilities"] = askingCapabilities
            snapshot["sessions"] = asking
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-groups") {
            snapshot["channels"] = [["name": "release", "members": ["demo", "codex-demo"], "memberDetails": [["id": "demo", "display": "@demo"], ["id": "codex-demo", "display": "@codex-demo"]]]]
            snapshot["relays"] = [["id": 7, "from": "demo", "to": "codex-demo", "pattern": "ready for review", "once": false, "active": true, "handoff": "turn_finished", "pendingCount": 0, "fireCount": 3, "lastFiredAt": "2026-09-12T03:58:00Z"]]
        }
        if SessionOperationsPreview.enabled {
            var operated = snapshot["sessions"] as! [[String: Any]]
            var operatedCapabilities: [String: Any] = capabilities
            SessionOperationsPreview.apply(session: &operated[0], capabilities: &operatedCapabilities)
            operated[0]["capabilities"] = operatedCapabilities
            snapshot["sessions"] = operated
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-terminal-question") {
            var terminal = snapshot["sessions"] as! [[String: Any]]
            var terminalCapabilities: [String: Any] = capabilities
            terminalQuestion(&terminal[0], capabilities: &terminalCapabilities)
            terminal[0]["attention"] = terminalAttention
            terminal[0]["capabilities"] = terminalCapabilities
            snapshot["sessions"] = terminal
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-project") {
            let stamp = ISO8601DateFormatter()
            func ago(_ minutes: Double) -> String { stamp.string(from: Date().addingTimeInterval(-minutes * 60)) }
            var timed = snapshot["sessions"] as! [[String: Any]]
            timed[0]["lastActive"] = ago(1)
            timed[0]["activity"] = ["busySince": ago(25), "lastProgressAt": ago(12)]
            timed[1]["lastActive"] = ago(3 * 24 * 60)
            timed[1]["current"] = true
            var recent = timed[1]
            recent["id"] = "notes-recent"
            recent["name"] = "Sort the field photos"
            recent["surface"] = "claude"
            recent["lastActive"] = ago(5 * 60)
            timed.append(recent)
            snapshot["sessions"] = timed
        }
        if TilesPreview.enabled { snapshot["projects"] = TilesPreview.projects }
        if ProjectNowPreview.scenario != nil {
            snapshot["sessions"] = ProjectNowPreview.sessions(capabilities: capabilities)
            snapshot["projects"] = [["id": ProjectNowPreview.projectID, "displayName": "fieldnotes", "checkouts": ProjectNowPreview.checkouts]]
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-unreachable") {
            var limited = snapshot["sessions"] as! [[String: Any]]
            limited[0]["waitingOn"] = previewUnreachable
            limited[0]["runtimeKind"] = "claude-host"
            var hostedCapabilities: [String: Any] = capabilities
            hostedCapabilities["respond"] = true
            hostedCapabilities["lifecycle"] = previewHostedLifecycle
            limited[0]["capabilities"] = hostedCapabilities
            limited[0]["attention"] = unreachableAttention
            limited[4]["usageLimit"] = previewUsageLimit
            limited[4]["attention"] = ["kind": "usageLimit", "reason": "Usage limit, resets 6:20 PM"]
            limited[4]["status"] = "idle"
            snapshot["sessions"] = limited
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-crowded") {
            var crowded = snapshot["sessions"] as! [[String: Any]]
            let idle = crowded[1]
            let tasks = ["Tidy the release notes", "Trim the onboarding copy", "Check the sync retries", "Rename the export flags", "Sort the crash reports", "Split the settings screen"]
            for (index, task) in tasks.enumerated() {
                var row = idle
                row["id"] = "crowded-\(index + 1)"
                row["name"] = task
                row["lastActive"] = "2026-09-1\(index)T09:00:00Z"
                crowded.append(row)
            }
            snapshot["sessions"] = crowded
        }
        if ProcessInfo.processInfo.arguments.contains("--preview-holders") {
            var sharedSessions = snapshot["sessions"] as! [[String: Any]]
            sharedSessions[0]["holders"] = [
                ["pid": 4101, "tty": "ttys001", "status": "busy", "startedAt": "2026-09-12T03:50:00Z"],
                ["pid": 4102, "tty": "ttys002", "status": "idle", "startedAt": "2026-09-12T03:58:00Z"]
            ]
            snapshot["sessions"] = sharedSessions
        }
        if AskPreview.enabled { AskPreview.apply(&snapshot) }
        return String(data: try! JSONSerialization.data(withJSONObject: snapshot), encoding: .utf8)!
    }

    nonisolated static let projectActivityJSON = #"{"projects":[]}"#
    nonisolated static let queueJSON = #"{"items":[{"id":1,"sessionId":"demo","target":"Make the build reliable","message":"Keep the regression check with the fix.","status":"pending","evidence":"queued","attempts":0,"queuedAt":"2026-09-12T04:00:00.000000000Z"},{"id":2,"sessionId":"codex-demo","target":"Review the release pipeline","message":"Verify the signing step before the next release.","status":"dead","evidence":"unknown","attempts":1,"lastError":"Delivery outcome is unknown. Check the session before retrying.","queuedAt":"2026-09-11T18:20:00.000000000Z"},{"id":3,"sessionId":"saved-demo","target":"Map the application architecture","message":"Old release reminder","status":"canceled","evidence":"canceled","attempts":0,"queuedAt":"2026-09-09T11:00:00.000000000Z"}]}"#

    private nonisolated static let steerRowCommon = #""sessionId":"demo","target":"Make the build reliable","status":"pending","evidence":"queued","attempts":0,"queuedAt":"2026-09-12T04:00:00.000000000Z","heldBy":"busy""#
    private nonisolated static let refusedSteerRow = #"{"id":12,\#(steerRowCommon),"message":"Then rerun the suite with the larger model.","model":"gpt-5.6-sol"}"#
    nonisolated static let steerRowsJSON = #"{"items":[{"id":11,\#(steerRowCommon),"message":"This is the failing screen, match the spacing.","attachments":[{"id":"upload:1","name":"failing-screen.png","mediaType":"image/png","bytes":2048,"width":800,"height":600}]},\#(refusedSteerRow)],"nextBefore":null}"#
    nonisolated static var tailRowsJSON: String {
        let common = #""sessionId":"demo","target":"Make the build reliable","evidence":"queued","attempts":0,"queuedAt":"2026-09-12T04:00:00.000000000Z""#
        func own(_ id: Int, _ message: String) -> String { #"{"id":\#(id),\#(common),"status":"pending","heldBy":"busy","author":"person","message":"\#(message)"}"# }
        let picture = #"{"id":21,\#(common),"status":"pending","heldBy":"busy","author":"person","message":"This is the failing screen, match the spacing.","attachments":[{"id":"upload:1","name":"failing-screen.png","mediaType":"image/png","bytes":2048,"width":800,"height":600}]}"#
        let rows: [String]
        switch UserDefaults.standard.string(forKey: "preview-tail") {
        case "none": rows = []
        case "review":
            let dead = #"{"id":26,"sessionId":"demo","target":"Make the build reliable","status":"dead","evidence":"unknown","attempts":1,"lastError":"Delivery outcome is unknown. Check the session before retrying.","author":"person","message":"Verify the signing step before the next release.","queuedAt":"2026-09-11T18:20:00.000000000Z"}"#
            rows = [own(22, "Keep the regression check with the fix."), dead]
        case "many": rows = [picture, own(22, "Then rerun the suite."), own(23, "Add a note to the changelog."), own(24, "Close the old issue once it's green."), own(25, "And tag the release.")]
        default: rows = [picture]
        }
        return #"{"items":[\#(rows.joined(separator: ","))],"nextBefore":null}"#
    }

    nonisolated static var previewSnapshotQueue: [[String: Any]] {
        guard ProcessInfo.processInfo.arguments.contains("--preview-tail"), !ProcessInfo.processInfo.arguments.contains("--preview-tail-late") else { return [] }
        let page = (try? JSONSerialization.jsonObject(with: Data(tailRowsJSON.utf8))) as? [String: Any]
        return (page?["items"] as? [[String: Any]]) ?? []
    }

    nonisolated static let steerRowsAfterSteerJSON = #"{"items":[\#(refusedSteerRow)],"nextBefore":null}"#

    nonisolated static let previewMetadataJSON = #"{"sessionId":"demo","claudeRuns":[{"recordPath":"/Users/demo/.claude/runs/job-1.json","jobId":"job-1","sessionId":"demo","runType":"bg","providerState":"working","createdAt":"2026-09-07T12:00:00Z","updatedAt":"2026-09-07T12:04:00Z"}],"claudeSubagents":[{"parentSessionId":"demo","agentId":"agent-1","transcriptPath":"/Users/demo/.claude/agents/agent-1.jsonl"}]}"#

    nonisolated static let detailJSON = #"""
    {
      "session":{"id":"demo","surface":"claude","name":"Make the build reliable","status":"busy","lastActive":"2026-09-07T12:05:00Z","cwd":"/Users/demo/projects/fieldnotes","source":"cli","transport":"local"},
      "journalSeq":2048,
      "exchanges":[],"capabilities":{"send":true,"stream":true,"reply":true,"goal":true,"goalSet":true,"goalStatus":true,"compact":true,"model":true,"interrupt":true,"steer":true,"attach":true,"permissionMode":true},
      "readOnly":false,"readOnlyReason":"","model":"Claude Sonnet",
      "models":[{"id":"sonnet","displayName":"Claude Sonnet"},{"id":"opus","displayName":"Claude Opus"}],
      "context":{"usedTokens":86400,"contextWindow":200000,"cumulativeTokens":312000,"compacting":false,"compactionCount":1,"reclaimedTokens":42000,"inputTokens":83000,"cachedInputTokens":60000,"outputTokens":3400},
      "goal":{"objective":"Fix the failing build and verify the release path","status":"active"},
      "timeline":{"source":"claude","truncated":false,"nextBefore":2400,"items":[
        {"id":"1","kind":"message","role":"user","title":"user","text":"The build fails after reconnecting. Trace it, fix it, and keep a regression test.","timestamp":"2026-09-07T12:00:00Z","truncated":false},
        {"id":"2","kind":"message","role":"assistant","title":"assistant · commentary","text":"The reconnect restores the conversation, but drops the pending build state. I’m checking the transition before changing it.","timestamp":"2026-09-07T12:01:00Z","truncated":false},
        {"id":"3","kind":"toolCall","title":"Bash","text":"{\"command\":\"swift test --filter ReconnectTests\",\"workdir\":\"/Users/demo/projects/fieldnotes\"}","callId":"build-1","timestamp":"2026-09-07T12:02:00Z","truncated":false},
        {"id":"4","kind":"toolResult","title":"Bash result","text":"Test Suite ReconnectTests passed.\nExecuted 8 tests, with 0 failures.","callId":"build-1","timestamp":"2026-09-07T12:03:00Z","truncated":false},
        {"id":"5","kind":"toolCall","title":"TodoWrite","text":"{\"todos\":[{\"content\":\"Trace reconnect state\",\"status\":\"completed\"},{\"content\":\"Verify the release build\",\"status\":\"in_progress\"}]}","timestamp":"2026-09-07T12:04:00Z","truncated":false}
      ]}
    }
    """#
}

private final class SessionPreviewProtocol: URLProtocol, @unchecked Sendable {
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    private let feed = PreviewStreamFeed()

    nonisolated(unsafe) private static var steered = false
    nonisolated(unsafe) private static var tailCleared = false
    nonisolated(unsafe) private static var sessionFetches = 0
    private var reopenDelayed = false
    private lazy var sentAction = Self.actionBody(request)
    private var olderDelayed = false

    private static func actionBody(_ request: URLRequest) -> [String: Any] {
        var data = request.httpBody
        if data == nil, let stream = request.httpBodyStream {
            stream.open()
            defer { stream.close() }
            var read = Data()
            var buffer = [UInt8](repeating: 0, count: 4096)
            while case let count = stream.read(&buffer, maxLength: buffer.count), count > 0 { read.append(buffer, count: count) }
            data = read
        }
        return data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:]
    }

    override func startLoading() {
        if SessionPreview.delaysOlderPage, !olderDelayed, request.url?.path == "/api/v1/session",
           URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.contains(where: { $0.name == "timelineBefore" && $0.value != "0" }) == true {
            olderDelayed = true
            DispatchQueue.global().asyncAfter(deadline: .now() + 3) { [self] in startLoading() }
            return
        }
        if request.url?.path == "/api/v1/session", ProcessInfo.processInfo.arguments.contains("--preview-reopen-slow"), !reopenDelayed {
            Self.sessionFetches += 1
            if Self.sessionFetches > 1 {
                reopenDelayed = true
                DispatchQueue.global().asyncAfter(deadline: .now() + 6) { [self] in startLoading() }
                return
            }
        }
        if request.url?.path == "/api/v1/queue", ProcessInfo.processInfo.arguments.contains("--preview-tail-slow") {
            let response = HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!
            DispatchQueue.global().asyncAfter(deadline: .now() + 5) { [self] in
                client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: Data(SessionPreview.tailRowsJSON.utf8))
                client?.urlProtocolDidFinishLoading(self)
            }
            return
        }
        if request.url?.path == "/api/v1/actions" {
            if ProcessInfo.processInfo.arguments.contains("--preview-lead-name") {
                client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 409, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: Data(#"{"error":{"code":"conflict","message":"lead"}}"#.utf8))
                client?.urlProtocolDidFinishLoading(self)
                return
            }
            if ProcessInfo.processInfo.arguments.contains("--preview-send-refused") {
                client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 409, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
                client?.urlProtocol(self, didLoad: Data(#"{"error":{"message":"This session has ended."}}"#.utf8))
                client?.urlProtocolDidFinishLoading(self)
                return
            }
        }
        let streamed = SessionCapture.current?.initialSessionID ?? "demo"
        let requestQuery = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        let requestID = requestQuery.first(where: { $0.name == "id" })?.value
        if PreviewStream.enabled, request.url?.path == "/api/v1/session", requestID == streamed,
           requestQuery.first(where: { $0.name == "timelineBefore" }) == nil {
            let base = SessionCapture.current?.detailJSON[streamed] ?? SessionPreview.previewDetailJSON.replacingOccurrences(of: "\"id\":\"demo\"", with: "\"id\":\"\(streamed)\"")
            let body = PreviewStream.detailJSON(base: base, captured: SessionCapture.current != nil)
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data(body.utf8))
            client?.urlProtocolDidFinishLoading(self)
            return
        }
        if PreviewStream.enabled, request.url?.path == "/api/v1/session-stream", requestID == streamed {
            let after = requestQuery.first(where: { $0.name == "after" })?.value.flatMap(UInt64.init) ?? 0
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: ["Content-Type": "text/event-stream"])!, cacheStoragePolicy: .notAllowed)
            feed.start(session: streamed, after: after, send: { [weak self] data in
                guard let self else { return }
                self.client?.urlProtocol(self, didLoad: data)
            }, drop: { [weak self] in
                guard let self else { return }
                self.client?.urlProtocol(self, didFailWithError: URLError(.networkConnectionLost))
            })
            return
        }
        var status = 200
        let body: String
        if let capture = SessionCapture.current {
            let query = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
            let id = query.first(where: { $0.name == "id" })?.value ?? ""
            let captured: String?
            switch request.url?.path {
            case "/api/v1/snapshot":
                if let parentID = query.first(where: { $0.name == "parentId" })?.value { captured = query.contains { $0.name == "cursor" } ? nil : capture.subagentPageJSON[parentID] }
                else if let scope = query.first(where: { $0.name == "scope" })?.value { captured = PreviewOrder.scoped(capture.snapshotJSON, scope, status: query.first { $0.name == "status" }?.value, surface: query.first { $0.name == "surface" }?.value) }
                else { captured = capture.snapshotJSON }
            case "/api/v1/queue": captured = capture.queueJSON
            case "/api/v1/inbox": captured = #"{"items":[]}"#
            case "/api/v1/projects/activity": captured = SessionPreview.projectActivityJSON
            case "/api/v1/session":
                let before = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "timelineBefore" })?.value
                captured = before == nil || before == "0" ? capture.detailJSON[id] : nil
            default: captured = nil
            }
            body = captured ?? #"{"error":{"message":"This local capture does not include this request or execute actions."}}"#
            status = captured == nil ? 503 : 200
        } else {
        switch request.url?.path {
        case "/api/v1/session-options": body = #"{"surfaces":[{"id":"claude","workspace":true},{"id":"codex","workspace":true},{"id":"notion","workspace":false}],"projects":[{"id":"p-fieldnotes","displayName":"fieldnotes","rootPath":"/Users/demo/projects/fieldnotes","kind":"git"}],"claudeConfigRoots":[{"name":"default","root":"/Users/demo/.claude","primary":true,"signedInAs":{"email":"demo@example.com"}},{"name":"team","root":"/Users/demo/.team/.claude","primary":false,"signedInAs":{"email":"team@example.com"}},{"name":"shared","root":"/Users/demo/.shared/.claude","primary":false,"login":"proxy","proxy":"shared"}]}"#
        case "/api/v1/session-options/claude-pick":
            body = #"{"pick":{"root":"/Users/demo/.team/.claude","name":"team","account":"team@example.com","reason":"your default root for new sessions"}}"#
        case "/api/v1/session-options/claude-usage":
            let root = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "root" })?.value ?? ""
            let (session, week) = root.hasSuffix(".team/.claude") ? (12, 30) : root.hasSuffix(".shared/.claude") ? (18, 41) : (40, 93)
            let best = root.hasSuffix(".shared/.claude") ? #","best":"ops@example.com""# : ""
            body = #"{"root":"\#(root)","usage":{"limits":[{"kind":"session","percent":\#(session)},{"kind":"weekly_all","percent":\#(week)}]}\#(best)}"#
        case "/api/v1/models":
            body = #"{"models":[{"id":"default","displayName":"Runtime default","default":true},{"id":"gpt-5.6-sol","displayName":"GPT-5.6-Sol","description":"Latest frontier agentic coding model.","supportedReasoningEfforts":["low","medium","high"],"defaultReasoningEffort":"low"},{"id":"chatgpt-web/pro","displayName":"ChatGPT Web — Pro","supportedReasoningEfforts":["ultra"],"defaultReasoningEffort":"ultra"},{"id":"gpt-5.5","displayName":"GPT-5.5","supportedReasoningEfforts":["medium","high"],"defaultReasoningEffort":"medium"},{"id":"gpt-5.3-codex-spark","displayName":"GPT-5.3 Codex Spark","supportedReasoningEfforts":["low","medium"],"defaultReasoningEffort":"low"},{"id":"provider/custom-runtime","displayName":"Provider custom runtime","description":"Provider supplied model ID","allowsCustom":true}]}"#
        case "/api/v1/session-commands":
            body = #"{"commands":[{"name":"review","description":"Review the current changes","argumentHint":"","insert":"/review"},{"name":"compact","description":"Summarize the conversation so far","argumentHint":"[instructions]","insert":"/compact"},{"name":"fieldnotes","description":"Write up today's notes","argumentHint":"","insert":"$fieldnotes"}],"unavailable":""}"#
        case "/api/v1/session-files":
            let query = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "q" })?.value ?? ""
            let files = [("Sources", true), ("Sources/Notebook.swift", false), ("Sources/NoteStore.swift", false), ("README.md", false)]
                .filter { query.isEmpty || $0.0.localizedCaseInsensitiveContains(query) }
                .map { #"{"path":"\#($0.0)","dir":\#($0.1)}"# }
            body = #"{"files":[\#(files.joined(separator: ","))],"truncated":false}"#
        case "/api/v1/snapshot" where PinPreview.enabled: body = PinPreview.apply(requestQuery.first(where: { $0.name == "scope" }).map { PreviewOrder.scoped(SessionPreview.snapshotJSON, $0.value, status: requestQuery.first { $0.name == "status" }?.value, surface: requestQuery.first { $0.name == "surface" }?.value) } ?? SessionPreview.snapshotJSON)
        case "/api/v1/actions" where PinPreview.handles(sentAction): body = PinPreview.respond(sentAction)
        case "/api/v1/snapshot": body = requestQuery.first(where: { $0.name == "scope" }).map { PreviewOrder.scoped(SessionPreview.snapshotJSON, $0.value, status: requestQuery.first { $0.name == "status" }?.value, surface: requestQuery.first { $0.name == "surface" }?.value) } ?? SessionPreview.snapshotJSON
        case "/api/v1/projects/activity": body = SessionPreview.projectActivityJSON
        case "/api/v1/actions" where SessionOperationsPreview.enabled:
            body = SessionOperationsPreview.respond(sentAction)
        case "/api/v1/actions" where ProcessInfo.processInfo.arguments.contains("--preview-project-message"):
            body = #"{"recipients":[{"sessionId":"demo","outcome":"delivered"}]}"#
        case "/api/v1/actions" where ProcessInfo.processInfo.arguments.contains("--preview-groups"):
            body = sentAction["action"] as? String == "channel-send" ? #"{"recipients":[{"sessionId":"demo","outcome":"delivered"},{"sessionId":"codex-demo","outcome":"queued"}]}"# : "{}"
        case "/api/v1/actions" where ProcessInfo.processInfo.arguments.contains("--preview-tail"):
            let action = sentAction
            if action["action"] as? String == "queue-cancel" { Self.tailCleared = !ProcessInfo.processInfo.arguments.contains("--preview-tail-keeps") }
            body = #"{"ok":true,"result":{"evidence":"submitted"}}"#
        case "/api/v1/queue" where ProcessInfo.processInfo.arguments.contains("--preview-tail-fails-after-action") && Self.tailCleared:
            status = 503; body = #"{"error":{"message":"unavailable"}}"#
        case "/api/v1/queue" where ProcessInfo.processInfo.arguments.contains("--preview-tail"):
            body = Self.tailCleared || requestQuery.first(where: { $0.name == "sessionId" })?.value == "codex-demo" ? #"{"items":[],"nextBefore":null}"# : SessionPreview.tailRowsJSON
        case "/api/v1/actions" where ProcessInfo.processInfo.arguments.contains("--preview-steer-rows"):
            let action = sentAction
            if action["action"] as? String == "queue-cancel" {
                body = #"{"ok":true,"result":{"message":"This is the failing screen, match the spacing.","attachments":[{"id":"upload:1","name":"failing-screen.png","mediaType":"image/png","bytes":2048,"width":800,"height":600}]}}"#
            } else if action["action"] as? String == "send", action["busyDelivery"] as? String == "steer" {
                Self.steered = true
                body = #"{"ok":true,"result":{"evidence":"submitted"}}"#
            } else {
                status = 400; body = #"{"error":{"message":"Not a steer."}}"#
            }
        case "/api/v1/queue" where ProcessInfo.processInfo.arguments.contains("--preview-steer-rows"): body = Self.steered ? SessionPreview.steerRowsAfterSteerJSON : SessionPreview.steerRowsJSON
        case "/api/v1/queue": body = SessionPreview.queueJSON
        case "/api/v1/inbox": body = InboxPreview.nextPage()
        case "/api/v1/requests" where AskPreview.enabled: body = AskPreview.requestsJSON
        case "/api/v1/actions" where AskPreview.enabled:
            let answer = AskPreview.answer(sentAction)
            status = answer.status
            body = answer.body
        case "/api/v1/requests" where ProcessInfo.processInfo.arguments.contains("--preview-questions"): body = SessionPreview.previewQuestionsJSON
        case "/api/v1/requests" where ProcessInfo.processInfo.arguments.contains("--preview-terminal-question"): body = SessionPreview.previewTerminalQuestionJSON
        case "/api/v1/decisions" where TilesPreview.enabled: body = TilesPreview.decisionsJSON
        case "/api/v1/decisions" where AskPreview.enabled: body = AskPreview.decisionsJSON
        case "/api/v1/usage": body = UsagePreview.json()
        case "/api/v1/proxies/prefer" where request.httpMethod == "GET":
            body = UsagePreview.preferPlan(only: URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "set" })?.value == "only")
        case "/api/v1/session-metadata": body = #"{"sessionId":"demo","claudeRuns":[{"recordPath":"/Users/demo/.claude/runs/job-1.json","jobId":"job-1","sessionId":"demo","runType":"bg","providerState":"working","createdAt":"2026-09-07T12:00:00Z","updatedAt":"2026-09-07T12:04:00Z"}],"claudeSubagents":[{"parentSessionId":"demo","agentId":"agent-1","transcriptPath":"/Users/demo/.claude/agents/agent-1.jsonl"}],"turnSettings":[{"key":"model","value":"sonnet","applies":"now","options":[{"id":"sonnet","label":"Claude Sonnet"},{"id":"opus","label":"Claude Opus"}]},{"key":"effort","value":"high","applies":"now","options":[{"id":"low","label":"Low"},{"id":"medium","label":"Medium"},{"id":"high","label":"High"},{"id":"max","label":"Max"}]},{"key":"mode","value":"acceptEdits","applies":"now","options":[{"id":"manual","label":"Manual"},{"id":"acceptEdits","label":"Accept edits"},{"id":"plan","label":"Plan"},{"id":"auto","label":"Auto"},{"id":"bypassPermissions","label":"Bypass permissions","confirm":true}]}],"compactSettings":[{"key":"autoCompact","value":"on","applies":"now","options":[{"id":"on","label":"On"},{"id":"off","label":"Off"}]},{"key":"compactWindow","value":"400000","applies":"newSessions","scope":"default","options":[{"id":"auto","label":"Auto"},{"id":"200000","label":"200k"},{"id":"400000","label":"400k"},{"id":"1000000","label":"1M"}]}]}"#
        case "/api/v1/session":
            let id = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "id" })?.value ?? "demo"
            let detail = SessionPreview.previewDetailJSON.replacingOccurrences(of: "\"id\":\"demo\"", with: "\"id\":\"\(id)\"")
            let older = SessionPreview.servesOlderPage && requestQuery.contains { $0.name == "timelineBefore" && $0.value != "0" }
            body = SessionPreview.servesOlderPage ? SessionPreview.olderPageDetail(base: detail, older: older) : detail
        default: status = 503; body = #"{"error":{"message":"This public preview does not execute actions."}}"#
        }
        }
        client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: nil, headerFields: ["Content-Type":"application/json"])!, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() { feed.stop() }
}
#endif
