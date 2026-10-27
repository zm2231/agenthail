import Combine
import Foundation

enum TimestampParser {
    static func date(_ value: String) -> Date? {
        guard let date = (try? fractional.parse(value)) ?? (try? wholeSecond.parse(value)), date.timeIntervalSince1970 > 0 else { return nil }
        return date
    }

    private static let fractional = Date.ISO8601FormatStyle(includingFractionalSeconds: true)
    private static let wholeSecond = Date.ISO8601FormatStyle()
}

struct ClaudeCreationSettings {
    var name = ""
    var worktree = ""
    var agent = ""
    var effort = ""
    var permissionMode = ""
    var configRoot = ""
    var fields: [String: String] {
        ["name": name, "worktree": worktree, "agent": agent, "effort": effort, "permissionMode": permissionMode, "configRoot": configRoot].filter { !$0.value.isEmpty }
    }
}

/// A Claude config directory and the login it holds now. The primary
/// directory is chosen by sending no config root.
struct ClaudeConfigRootOption: Decodable, Hashable, Identifiable {
    struct Login: Decodable, Hashable {
        var email: String? = nil
    }
    let name: String
    let root: String
    let primary: Bool
    var signedInAs: Login? = nil
    var usage: ProviderUsage? = nil
    var id: String { root }
    var choice: String { primary ? "" : root }
    var signedIn: Bool { signedInAs != nil }

    func exhaustedUntil(now: Date = Date()) -> Date? {
        let resets = (usage?.limits ?? []).filter { $0.percent >= 100 }.compactMap { limit in
            limit.resetsAt.flatMap(TimestampParser.date)
        }.filter { $0 > now }
        return resets.max()
    }

    var menuLabel: String {
        var parts = [name, signedInAs.map { "signed in as \($0.email ?? "unknown")" } ?? "signed out"]
        if let until = exhaustedUntil() {
            parts.append("out of usage until \(until.formatted(date: .abbreviated, time: .shortened))")
        }
        return parts.joined(separator: " · ")
    }
}

struct SessionAdoptResult: Decodable, Hashable {
    var stopped: String? = nil
    var unchanged: Bool? = nil

    var summary: String {
        if unchanged == true { return "Already runs in Agenthail." }
        guard let stopped else { return "Opened in Agenthail." }
        return "Opened in Agenthail. Stopped the \(stopped) that held it."
    }
}

struct APIErrorEnvelope: Decodable {
    let code: String
    let message: String
}

struct SessionAdoptReply: Decodable {
    let ok: Bool?
    var result: SessionAdoptResult? = nil
    var error: APIErrorEnvelope? = nil
    var busy: String? = nil
}

enum SessionAdoptOutcome: Equatable {
    case adopted(SessionAdoptResult)
    case needsConsent(String)
    case busy(String)
}

/// Drives opening a terminal or background Claude session in Agenthail for
/// the Mac and iPhone sheets. Stopping a terminal process always waits for
/// the person to confirm it.
@MainActor
final class SessionAdoptController: ObservableObject {
    enum Phase: Equatable {
        case ready
        case adopting
        case confirmTerminal(String)
        case done(String)
        case failed(String)
    }

    @Published private(set) var phase: Phase = .ready
    let session: SessionState

    init(session: SessionState) {
        self.session = session
    }

    var canAdopt: Bool { phase == .ready && session.canAdopt }

    func adopt(api: AgenthailAPI, stopTerminal: Bool = false) async {
        guard session.canAdopt else { return }
        phase = .adopting
        do {
            switch try await api.adoptSession(id: session.id, stopTerminal: stopTerminal) {
            case .adopted(let result): phase = .done(result.summary)
            case .needsConsent(let message): phase = .confirmTerminal(message)
            case .busy(let reason): phase = .failed("The session is busy: \(reason). Open it in Agenthail when it is idle.")
            }
        } catch {
            phase = .failed(AgenthailAPIError.userMessage(error))
        }
    }

    func cancelConfirmation() { phase = .ready }
}

/// The config directory holding a Claude session's transcript, with the
/// login that directory holds now.
struct SessionConfigRoot: Codable, Hashable {
    let name: String
    let root: String
    var signedInAs: String? = nil

    var summary: String {
        guard let signedInAs, !signedInAs.isEmpty else { return name }
        return "\(name) · currently signed in as \(signedInAs)"
    }
}

/// One config root's current usage, read from claude.ai after the new-session
/// form has shown the roots.
/// An account's limits as its provider last reported them, at fetchedAt.
struct ProviderUsage: Decodable, Hashable {
    struct Limit: Decodable, Hashable {
        let kind: String
        let percent: Double
        var resetsAt: String? = nil
    }
    var fetchedAt: String? = nil
    var limits: [Limit]? = nil
}

struct ClaudeRootUsage: Decodable {
    let root: String
    var usage: ProviderUsage? = nil
    var usageProblem: String? = nil
}

struct UsageReport: Decodable, Hashable {
    struct Meter: Decodable, Hashable {
        let provider: String
        let kind: String
        let label: String
        var scope: String? = nil
        let percent: Double
        var severity: String? = nil
        var resetsAt: String? = nil
        let asOf: String
        let aged: Bool
        var until: String? = nil
    }
    struct Set: Decodable, Hashable {
        let host: String
        let hostId: String
        var local: Bool? = nil
        var provider: String? = nil
        var name: String? = nil
        var account: String? = nil
        var login: String? = nil
        var root: String? = nil
        var primary: Bool? = nil
        var meters: [Meter]? = nil
        var asOf: String? = nil
        var problem: String? = nil
        var problemDetail: String? = nil
    }
    var sets: [Set]? = nil
}

struct SessionCreationOptions: Decodable {
    struct Surface: Decodable, Identifiable { let id: String; let workspace: Bool; var connected: Bool? = nil }
    var host: SessionHost? = nil
    let surfaces: [Surface]
    let projects: [LaunchProject]
    var launchers: [LauncherOption]? = nil
    var claudeConfigRoots: [ClaudeConfigRootOption]? = nil
    var folders: [String]? = nil
}

/// A project a new session can start in, most recently active first. The
/// root path is where the session starts.
struct LaunchProject: Decodable, Hashable, Identifiable {
    let id: String
    let displayName: String
    let rootPath: String
    let kind: String
    var added: Bool? = nil
}

struct CreationHost: Hashable, Identifiable {
    let id: String
    let name: String
    let state: String
    var isLocal: Bool { id.isEmpty }
    var isAvailable: Bool { state == "connected" }
    var stateLabel: String { HostHealth(id: id, name: name, state: state).stateLabel }
}

/// Where a new session starts and the launch catalog read from that host.
/// An empty host id is this machine. Loads carry the generation they were
/// started for, so an answer for a host the user already left is dropped.
struct CreationHostPicker {
    private(set) var hosts: [CreationHost] = []
    private(set) var selected = ""
    private(set) var options: SessionCreationOptions?
    private(set) var models: [String: [ModelOption]] = [:]
    private(set) var loadError: String?
    private(set) var generation = 0

    init(daemon: DaemonState?, selectedSession: SessionState?) {
        update(daemon: daemon)
        if let host = selectedSession?.host, host.state != "local", hosts.contains(where: { $0.id == host.id && $0.isAvailable }) {
            selected = host.id
        }
    }

    var isVisible: Bool { hosts.count > 1 }
    var selectedHost: CreationHost? { hosts.first { $0.id == selected } }
    var hostQuery: String? { selected.isEmpty ? nil : selected }
    var isLoading: Bool { options == nil && loadError == nil }
    var canStart: Bool { options != nil && (selected.isEmpty || selectedHost?.isAvailable == true) }
    var projects: [LaunchProject] { options?.projects ?? [] }
    var claudeConfigRoots: [ClaudeConfigRootOption] { options?.claudeConfigRoots ?? [] }

    init(hosts: [CreationHost], selectedSession: SessionState?) {
        update(hosts: hosts)
        if let id = selectedSession?.host?.id, self.hosts.contains(where: { $0.id == id && $0.isAvailable }) {
            selected = id
        }
    }

    mutating func update(daemon: DaemonState?) {
        let paired = daemon?.hosts ?? []
        if let local = daemon?.host, !paired.isEmpty {
            update(hosts: [CreationHost(id: "", name: local.name, state: "connected")] + paired.map { CreationHost(id: $0.id, name: $0.name, state: $0.state) })
        } else {
            update(hosts: [])
        }
    }

    mutating func update(hosts next: [CreationHost]) {
        hosts = next
        guard selectedHost == nil else { return }
        let fallback = hosts.first { $0.isLocal || $0.isAvailable }?.id ?? ""
        guard !(selected.isEmpty && fallback.isEmpty) else { return }
        selected = fallback
        options = nil
        models = [:]
        loadError = nil
        generation += 1
    }

    @discardableResult
    mutating func select(_ id: String) -> Bool {
        guard id != selected, hosts.contains(where: { $0.id == id && $0.isAvailable }) else { return false }
        selected = id
        options = nil
        models = [:]
        loadError = nil
        generation += 1
        return true
    }

    func owns(_ session: SessionState?) -> Bool {
        guard let session else { return false }
        return (session.remoteHostName == nil ? "" : session.host?.id ?? "") == selected
    }

    /// Where a new session starts by default: the selected session's checkout
    /// when it is on the chosen host, else that host's most recent project.
    func defaultFolder(selected session: SessionState?) -> String {
        (owns(session) ? session.flatMap { $0.checkout?.path ?? $0.cwd } : nil) ?? projects.first?.rootPath ?? ""
    }

    mutating func receive(options next: SessionCreationOptions, for loaded: Int) {
        guard loaded == generation else { return }
        options = next
        loadError = nil
    }

    mutating func receive(usage next: ClaudeRootUsage, for loaded: Int) {
        guard loaded == generation, let index = options?.claudeConfigRoots?.firstIndex(where: { $0.root == next.root }) else { return }
        options?.claudeConfigRoots?[index].usage = next.usage
    }

    mutating func receive(models next: [ModelOption], agent: String, for loaded: Int) {
        guard loaded == generation else { return }
        models[agent] = next
    }

    mutating func fail(_ message: String, for loaded: Int) {
        guard loaded == generation else { return }
        loadError = message
    }
}
struct CreationModels: Decodable { let models: [ModelOption] }
struct QueueResponse: Decodable { let items: [QueueState] }
struct SessionCreationReceipt: Decodable {
    let ok: Bool?
    let status: String?
    let accepted: Bool?
    let warning: String?
    let session: RawSession?
    let sessionId: String?
    let error: APIErrorEnvelope?
    let launcher: String?
    let location: SessionRuntime.Location?
    var id: String? { session?.id ?? sessionId }
    var outcomeUnknown: Bool { ["create_unknown", "host_outcome_unknown"].contains(error?.code) }
    static let unknownOutcomeMessage = "Agenthail can't tell whether this session started. Check the sidebar before trying again."
}

struct APIVersion: Decodable {
    let protocolVersion: Int
    let minimumProtocol: Int
    let maximumProtocol: Int
    let pushRelayUrl: String?
    var version: String? = nil
    var revision: String? = nil
    var modified: Bool? = nil

    enum CodingKeys: String, CodingKey {
        case protocolVersion = "protocol"
        case minimumProtocol
        case maximumProtocol
        case pushRelayUrl
        case version
        case revision
        case modified
    }

    /// The build the daemon runs; nil for a daemon too old to say.
    var build: AgenthailBuild? {
        version.map { AgenthailBuild(version: $0, revision: revision ?? "unknown", modified: modified ?? false) }
    }
}

/// The build an Agenthail daemon or app came from.
struct AgenthailBuild: Codable, Hashable {
    let version: String
    let revision: String
    var modified = false

    /// v0.2.68 (e387b0d8), as doctor names a build.
    var label: String {
        "\(version) (\(revision.prefix(8))\(modified ? ", modified" : ""))"
    }

    var revisionKnown: Bool { !revision.isEmpty && revision != "unknown" }

    /// The build this app was packaged from, or nil when it was built without
    /// naming its commit.
    static func app(_ info: [String: Any]?) -> AgenthailBuild? {
        guard let revision = info?["AgenthailRevision"] as? String, !revision.isEmpty else { return nil }
        let version = info?["CFBundleShortVersionString"] as? String ?? "unknown"
        return AgenthailBuild(version: version, revision: revision, modified: info?["AgenthailRevisionModified"] as? Bool ?? false)
    }

    /// What to tell the person when the daemon this app runs with is another
    /// build. Builds that do not both name an unmodified commit are not compared.
    static func daemonNotice(app: AgenthailBuild?, daemon: AgenthailBuild?) -> String? {
        guard let app, let daemon, app.revisionKnown, daemon.revisionKnown, !app.modified, !daemon.modified,
              app.revision != daemon.revision else { return nil }
        return "The running daemon is \(daemon.label); this app is \(app.label). Restart Agenthail to use this version."
    }
}

struct Capabilities: Codable, Hashable {
    var send = false
    var stream = false
    var reply = false
    var goal = false
    var goalSet = false
    var compact = false
    var model = false
    var interrupt = false
    var steer = false
    var attach = false
    var commands = false
    var respond = false
    var permissionMode = false
    /// Why what the person writes reaches the session only as a note from
    /// Agenthail, and what would make it their own turn; empty when it is.
    var turnNotice = ""
    var lifecycle = SessionLifecycle()

    init(send: Bool = false, stream: Bool = false, reply: Bool = false, goal: Bool = false, goalSet: Bool = false, compact: Bool = false, model: Bool = false, interrupt: Bool = false, steer: Bool = false, attach: Bool = false, commands: Bool = false, respond: Bool = false, permissionMode: Bool = false, turnNotice: String = "", lifecycle: SessionLifecycle = SessionLifecycle()) {
        (self.send, self.stream, self.reply, self.goal, self.goalSet, self.compact, self.model, self.interrupt, self.steer, self.attach, self.commands, self.respond, self.permissionMode, self.turnNotice, self.lifecycle) = (send, stream, reply, goal, goalSet, compact, model, interrupt, steer, attach, commands, respond, permissionMode, turnNotice, lifecycle)
    }

    // A paired host on an older release omits goalSet, attach, commands,
    // respond, permissionMode, turnNotice and lifecycle; it cannot do any of
    // them and has no notice to give.
    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        send = try container.decode(Bool.self, forKey: .send)
        stream = try container.decode(Bool.self, forKey: .stream)
        reply = try container.decode(Bool.self, forKey: .reply)
        goal = try container.decode(Bool.self, forKey: .goal)
        goalSet = try container.decodeIfPresent(Bool.self, forKey: .goalSet) ?? false
        compact = try container.decode(Bool.self, forKey: .compact)
        model = try container.decode(Bool.self, forKey: .model)
        interrupt = try container.decode(Bool.self, forKey: .interrupt)
        steer = try container.decode(Bool.self, forKey: .steer)
        attach = try container.decodeIfPresent(Bool.self, forKey: .attach) ?? false
        commands = try container.decodeIfPresent(Bool.self, forKey: .commands) ?? false
        respond = try container.decodeIfPresent(Bool.self, forKey: .respond) ?? false
        permissionMode = try container.decodeIfPresent(Bool.self, forKey: .permissionMode) ?? false
        turnNotice = try container.decodeIfPresent(String.self, forKey: .turnNotice) ?? ""
        lifecycle = try container.decodeIfPresent(SessionLifecycle.self, forKey: .lifecycle) ?? SessionLifecycle()
    }
}

/// The operations on a session's process that its runtime supports.
struct SessionLifecycle: Codable, Hashable {
    var status = false
    var logs = false
    var stop = false
    var resume = false
    /// The session can be reopened in Agenthail's host.
    var adopt = false

    init(status: Bool = false, logs: Bool = false, stop: Bool = false, resume: Bool = false, adopt: Bool = false) {
        (self.status, self.logs, self.stop, self.resume, self.adopt) = (status, logs, stop, resume, adopt)
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        status = try container.decodeIfPresent(Bool.self, forKey: .status) ?? false
        logs = try container.decodeIfPresent(Bool.self, forKey: .logs) ?? false
        stop = try container.decodeIfPresent(Bool.self, forKey: .stop) ?? false
        resume = try container.decodeIfPresent(Bool.self, forKey: .resume) ?? false
        adopt = try container.decodeIfPresent(Bool.self, forKey: .adopt) ?? false
    }

    /// Any operation on the session's own process. Adoption is offered on its
    /// own, not in the process operations.
    var any: Bool { status || logs || stop || resume }
}

struct SurfaceState: Decodable, Identifiable, Equatable {
    var id: String { name }
    let name: String
    let connected: Bool
    let error: String?
    let health: String
    let healthDetail: String?
    let capabilities: Capabilities
    var runtime: SurfaceRuntime? = nil
    var repairAction: String? = nil
    var repairLabel: String? = nil

    /// Browser sign-in turned off, or a computer with no browser: states the
    /// surface reports, not problems to act on.
    var isStatedLimit: Bool { health == "signin_off" || health == "signin_unavailable" }
    var needsAttention: Bool { (health != "healthy" && !isStatedLimit) || !(runtime?.advice.isEmpty ?? true) }
}

struct SurfaceRuntimeNote: Decodable, Equatable {
    let problem: String
    let message: String
    let remediation: String?
}

struct SurfaceRuntime: Decodable, Equatable {
    let name: String
    let reachable: Bool
    let durable: Bool
    let problem: String?
    let detail: String?
    let remediation: String?
    let notes: [SurfaceRuntimeNote]?

    var advice: [String] {
        var lines: [String] = []
        if problem != nil {
            lines.append([detail, remediation.map { "Fix: \($0)" }].compactMap { $0 }.joined(separator: ". "))
        }
        for note in notes ?? [] {
            lines.append(note.remediation.map { "\(note.message). Fix: \($0)" } ?? note.message)
        }
        return lines
    }
}

struct SessionState: Codable, Identifiable, Hashable {
    var id: String
    let surface: String
    let name: String
    let alias: String?
    let status: String
    let lastActive: String?
    var queueCount: Int
    let open: Bool
    var current: Bool
    var currentReason: String?
    var archived: Bool? = nil
    let capabilities: Capabilities
    let readOnly: Bool?
    let readOnlyReason: String?
    var cwd: String? = nil
    var project: ProjectIdentity? = nil
    var checkout: CheckoutIdentity? = nil
    var runtime: SessionRuntime? = nil
    var subagent: SubagentIdentity? = nil
    var subagents: SubagentRollup? = nil
    var hiddenSubagents: SubagentRollup? = nil
    var listed: Bool? = nil
    var host: SessionHost? = nil
    var unavailableReason: String? = nil
    var transport: String? = nil
    var waitingOn: WaitingState? = nil
    var backgroundTasks: BackgroundTasks? = nil
    var usageLimit: UsageLimitState? = nil
    var limitResume: LimitResumeState? = nil
    var activity: SessionActivity? = nil
    var plan: SessionPlan? = nil
    var holders: [SessionHolder]? = nil
    var claudeConfigRoot: SessionConfigRoot? = nil
    var runtimeKind: String? = nil
    /// The turn notice, with the app's own remedy when it can adopt the session.
    var composerTurnNotice: String {
        let notice = capabilities.turnNotice
        guard !notice.isEmpty, canAdopt else { return notice }
        return notice + " Open in Agenthail also makes it your own turn."
    }
    /// A session its daemon can open in Agenthail's host.
    var canAdopt: Bool { capabilities.lifecycle.adopt && !isReadOnly }

    var displayName: String {
        if let alias, !alias.isEmpty { return "@\(alias)" }
        return name.isEmpty ? id : name
    }

    var isWorking: Bool { status == "busy" }
    /// "Working", with how long the turn has been quiet once it has been.
    func workingLabel(now: Date) -> String {
        activity?.quietNote(now: now).map { "Working · " + $0 } ?? "Working"
    }
    var isWaitingOnYou: Bool { waitingOn != nil }
    /// The task list's progress while the session is open and has steps
    /// left. The row keeps the plan after the session closes; a session that
    /// stopped part way shows no badge.
    var livePlan: SessionPlan? {
        guard open, status != "offline", !isUnavailable, let plan, plan.total > 0, plan.done < plan.total else { return nil }
        return plan
    }
    var waitingPlace: WaitingPlace? {
        waitingOn.map { WaitingPlace($0, runtimeKind: runtimeKind, lifecycle: isReadOnly ? SessionLifecycle() : capabilities.lifecycle) }
    }
    /// Between turns with background tasks still running: not finished yet.
    var isWaitingOnBackground: Bool { !isWorking && backgroundTasks?.running == true }
    /// What a usage limit did to the session: when it resumes, or that it stays stopped.
    var limitLabel: String? { limitResume?.label ?? usageLimit?.label }
    var isReadOnly: Bool { readOnly == true }
    var remoteHostName: String? {
        guard let host, host.state != "local" else { return nil }
        return host.name
    }
    /// The remote host's name without a DNS domain, for a one-line badge.
    var shortRemoteHostName: String? {
        remoteHostName.map { name in String(name.split(separator: ".", maxSplits: 1).first ?? Substring(name)) }
    }
    var isUnavailable: Bool { remoteHostName != nil && !(unavailableReason ?? "").isEmpty }
    /// The paired host that owns this session, or nil for this machine.
    var remoteHostID: String? { remoteHostName == nil ? nil : host?.id }
    var modelCatalogKey: String { "\(remoteHostID ?? "")/\(surface)" }
    var canCallByVoice: Bool { remoteHostID == nil }
}

struct SessionHost: Codable, Hashable {
    let id: String
    let name: String
    let state: String
}

struct HostHealth: Decodable, Hashable, Identifiable {
    let id: String
    let name: String
    let state: String
    var detail: String? = nil
    var notice: String? = nil
    var build: AgenthailBuild? = nil

    var stateLabel: String {
        switch state {
        case "connected": return "Connected"
        case "connecting": return "Connecting"
        case "unreachable": return "Unreachable"
        case "unauthorized": return "Pairing revoked"
        case "incompatible": return "Needs update"
        default: return state.capitalized
        }
    }
    var isHealthy: Bool { state == "connected" }
}

/// What a session runs in the background. Ambient tasks, such as file
/// watchers, are listed but do not keep it running.
struct BackgroundTasks: Codable, Hashable {
    let running: Bool
    var tasks: [BackgroundTask]? = nil
    var since: String? = nil
    let source: String

    var activeCount: Int { (tasks ?? []).filter { $0.ambient != true }.count }
    var summary: String {
        if !running { return "Only ambient tasks run" }
        let count = activeCount
        return count == 0 ? "Background work running" : "\(count) background task\(count == 1 ? "" : "s") running"
    }
    /// Where the list came from, when it is not Claude's own report.
    var sourceNote: String? {
        switch source {
        case "transcript": return "Read from the transcript."
        case "status": return "Claude reports a background shell but not which one."
        default: return nil
        }
    }
}

struct BackgroundTask: Codable, Hashable, Identifiable {
    let id: String
    let kind: String
    var description: String? = nil
    var parentId: String? = nil
    var ambient: Bool? = nil
    var since: String? = nil

    var kindLabel: String {
        switch kind {
        case "shell": return "Shell"
        case "agent": return "Agent"
        case "monitor": return "Monitor"
        default: return kind.capitalized
        }
    }
    var line: String {
        let text = description.flatMap { $0.isEmpty ? nil : $0 } ?? id
        return ambient == true ? "\(kindLabel): \(text) (ambient)" : "\(kindLabel): \(text)"
    }
}

/// A question, plan or approval the session is waiting on its user to answer.
/// An answerable one is held by Agenthail and answered here.
struct WaitingState: Codable, Hashable {
    let kind: String
    let id: String
    var since: String? = nil
    var answerable: Bool? = nil
}

/// Where a session's wait is answered: here, in its terminal, or wherever
/// else it runs. A hosted or background session's prompt that Agenthail does
/// not hold has nowhere to answer it; stopping and resuming the session
/// clears it.
enum WaitingPlace: Equatable {
    case agenthail, dialog, terminal, restart, elsewhere

    init(_ waiting: WaitingState, runtimeKind: String?, lifecycle: SessionLifecycle) {
        if waiting.answerable == true {
            self = .agenthail
        } else if waiting.kind == "dialog" {
            self = .dialog
        } else if lifecycle.stop && lifecycle.resume {
            self = .restart
        } else if runtimeKind == "claude-cli" {
            self = .terminal
        } else {
            self = .elsewhere
        }
    }

    /// The session's status while it waits here.
    var label: String {
        switch self {
        case .agenthail, .elsewhere: return "Waiting on you"
        case .dialog: return "Dialog open in its terminal"
        case .terminal: return "Waiting in its terminal"
        case .restart: return "Prompt out of reach"
        }
    }
}

/// A session whose last turn a usage limit refused, and when it lifts.
struct UsageLimitState: Codable, Hashable {
    var resetsAt: String? = nil

    var label: String {
        guard let resetsAt, let date = TimestampParser.date(resetsAt) else { return "Usage limit reached" }
        return "Usage limit, resets \(date.formatted(date: .omitted, time: .shortened))"
    }
}

/// A session a usage limit stopped, as its computer resumes it: waiting for
/// the reset, or kept stopped (canceled, or its reset passed long ago).
struct LimitResumeState: Codable, Hashable {
    var state: String
    var refusedAt: String? = nil
    var resetsAt: String? = nil

    var isWaiting: Bool { state == "waiting" }

    var label: String {
        guard isWaiting else { return "Stopped by usage limit" }
        guard let resetsAt, let date = TimestampParser.date(resetsAt) else { return "Usage limit, resumes when it lifts" }
        let time = Calendar.current.isDateInToday(date) ? date.formatted(date: .omitted, time: .shortened) : date.formatted(.dateTime.weekday(.abbreviated).hour().minute())
        return "Usage limit, resumes \(time)"
    }
}

/// How a working session's turn is going, as its daemon reads it. It never
/// changes the session's status; it only lets a quiet turn say how long it
/// has been quiet and what it is running.
struct SessionActivity: Codable, Hashable {
    struct RunningTool: Codable, Hashable {
        let name: String
        var startedAt: String? = nil
    }

    var busySince: String? = nil
    var lastProgressAt: String? = nil
    var tool: RunningTool? = nil
    var noProcess: Bool? = nil

    /// How long a working turn stays quiet before its row says so.
    static let quietAfter: TimeInterval = 10 * 60

    /// "no activity for 12 min · running Bash since 10:02" once the turn has
    /// been quiet for quietAfter, or nil before.
    func quietNote(now: Date) -> String? {
        guard let raw = lastProgressAt ?? busySince, let last = SessionTree.parseTimestamp(raw) else { return nil }
        let quiet = now.timeIntervalSince(last)
        guard quiet >= Self.quietAfter else { return nil }
        var parts = ["no activity for " + Self.duration(quiet)]
        if noProcess == true {
            parts.append("no Claude process")
        } else if let tool {
            parts.append("running " + tool.name + (tool.startedAt.flatMap(SessionTree.parseTimestamp).map { " since " + $0.formatted(date: .omitted, time: .shortened) } ?? ""))
        }
        return parts.joined(separator: " · ")
    }

    static func duration(_ interval: TimeInterval) -> String {
        let minutes = Int(interval / 60)
        guard minutes >= 60 else { return "\(minutes) min" }
        return minutes % 60 == 0 ? "\(minutes / 60) hr" : "\(minutes / 60) hr \(minutes % 60) min"
    }
}

/// How far a session is through the task list its agent keeps, as its
/// daemon reads it. current is the step in progress, when one is.
struct SessionPlan: Codable, Hashable {
    let done: Int
    let total: Int
    var current: String? = nil

    var badge: String { "\(done)/\(total)" }
    var spokenLabel: String { "\(done) of \(total) tasks done" }
    var detail: String { current.map { spokenLabel + " · " + $0 } ?? spokenLabel }
}

struct SubagentIdentity: Codable, Hashable {
    let parentId: String
    let rootId: String
    let depth: Int
    var nickname: String? = nil
    var role: String? = nil
}

struct SubagentRollup: Codable, Hashable {
    let count: Int
    let working: Int
}

struct SessionFamily: Identifiable, Equatable {
    struct Member: Identifiable, Equatable {
        let session: SessionState
        let depth: Int
        var id: String { session.id }
    }

    let root: SessionState
    let members: [Member]
    let workingSubagents: Int

    var id: String { root.id }
    var isWorking: Bool { root.isWorking || workingSubagents > 0 }
    var isCurrent: Bool { isWorking || sessions.contains(where: \.current) }
    var sessions: [SessionState] { [root] + members.map(\.session) }

    func contains(_ sessionID: String?) -> Bool {
        guard let sessionID else { return false }
        return root.id == sessionID || members.contains { $0.id == sessionID }
    }
}

/// Refetches a session's subagents when it changes, its subagent counts do,
/// or the catalog reports a change among its hidden subagents.
struct SubagentFetchKey: Hashable {
    let sessionID: String
    let subagents: SubagentRollup?
    let hidden: SubagentRollup?
    let revision: Int

    init(_ session: SessionState, revision: Int) {
        sessionID = session.id
        subagents = session.subagents
        hidden = session.hiddenSubagents
        self.revision = revision
    }
}

/// The fetched subagent pages of the few sessions most recently shown; a
/// parent's page can run to hundreds of kilobytes.
struct SubagentPageCache {
    static let capacity = 4
    private(set) var errors: [String: String] = [:]
    private var rows: [String: [SessionState]] = [:]
    private var order: [String] = []
    private var revisions: [String: Int] = [:]

    func children(of parentID: String) -> [SessionState] { rows[parentID] ?? [] }
    func revision(_ parentID: String) -> Int { revisions[parentID] ?? 0 }
    func loading(_ parent: SessionState) -> Bool { (parent.hiddenSubagents?.count ?? 0) > 0 && rows[parent.id] == nil && errors[parent.id] == nil }
    func session(_ id: String) -> SessionState? { rows.values.lazy.flatMap { $0 }.first { $0.id == id } }

    mutating func store(_ sessions: [SessionState], for parentID: String) {
        rows[parentID] = sessions
        errors[parentID] = nil
        touch(parentID)
    }

    mutating func fail(_ message: String, for parentID: String) {
        errors[parentID] = message
        touch(parentID)
    }

    func holds(_ parentID: String) -> Bool { rows[parentID] != nil || errors[parentID] != nil }

    mutating func refetch(_ parentID: String) {
        rows[parentID] = nil
        errors[parentID] = nil
        revisions[parentID, default: 0] += 1
    }

    mutating func drop(_ parentID: String) {
        rows[parentID] = nil
        errors[parentID] = nil
        revisions[parentID] = nil
        order.removeAll { $0 == parentID }
    }

    /// A device's catalog stream reports a hidden subagent's arrival or change
    /// only as an unlisted removal without its parent, so an unknown one
    /// refetches every cached page.
    mutating func sessionRemoved(_ id: String, unlisted: Bool) {
        drop(id)
        let parents = rows.filter { $0.value.contains { $0.id == id } }.map(\.key)
        for parent in parents { rows[parent]?.removeAll { $0.id == id } }
        for parent in parents.isEmpty && unlisted ? Array(rows.keys) : parents {
            revisions[parent, default: 0] += 1
        }
    }

    private mutating func touch(_ parentID: String) {
        order.removeAll { $0 == parentID }
        order.append(parentID)
        while order.count > Self.capacity {
            let evicted = order.removeFirst()
            rows[evicted] = nil
            errors[evicted] = nil
            revisions[evicted] = nil
        }
    }
}

enum SessionFamilies {
    static func build(_ sessions: [SessionState]) -> [SessionFamily] {
        let ids = Set(sessions.map(\.id))
        var children: [String: [SessionState]] = [:]
        var roots: [SessionState] = []
        for session in sessions {
            if let parent = session.subagent?.parentId, parent != session.id, ids.contains(parent) {
                children[parent, default: []].append(session)
            } else {
                roots.append(session)
            }
        }
        var placed: Set<String> = []
        return roots.map { root in
            placed.insert(root.id)
            var members: [SessionFamily.Member] = []
            func walk(_ parentID: String, depth: Int) {
                for child in children[parentID] ?? [] where !placed.contains(child.id) {
                    placed.insert(child.id)
                    members.append(SessionFamily.Member(session: child, depth: depth))
                    walk(child.id, depth: depth + 1)
                }
            }
            walk(root.id, depth: 1)
            let observed = root.subagents ?? SubagentRollup(count: 0, working: 0)
            let counted = members.filter { $0.session.listed != false }
            return SessionFamily(
                root: root,
                members: members,
                workingSubagents: counted.filter { $0.session.isWorking }.count + observed.working
            )
        }
    }

    /// The direct subagents of a session: the fetched page, with any row the
    /// live session list holds taking the place of its fetched copy.
    static func children(of parentID: String, listed: [SessionState], fetched: [SessionState]) -> [SessionState] {
        let live = listed.filter { $0.subagent?.parentId == parentID }
        let liveIDs = Set(live.map(\.id))
        return fetched.filter { $0.subagent?.parentId == parentID && !liveIDs.contains($0.id) } + live
    }

    static func subagentSummary(_ family: SessionFamily) -> String {
        "\(family.workingSubagents) \(family.workingSubagents == 1 ? "subagent" : "subagents") working"
    }

    static func label(_ session: SessionState) -> String {
        guard let nickname = session.subagent?.nickname, !nickname.isEmpty else { return session.title }
        if let role = session.subagent?.role, !role.isEmpty { return "\(nickname) (\(role))" }
        return nickname
    }

    static func title(_ session: SessionState, in sessions: [SessionState]) -> String {
        guard let parentID = session.subagent?.parentId else { return session.title }
        guard let parent = sessions.first(where: { $0.id == parentID }) else { return label(session) }
        return "\(title(parent, in: sessions)) > \(label(session))"
    }
}

extension SessionState {
    var title: String {
        if let alias, !alias.isEmpty { return "@\(alias)" }
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty || trimmed == id || UUID(uuidString: trimmed) != nil { return "Untitled conversation" }
        return trimmed
    }
}

struct SessionRuntime: Codable, Hashable {
    struct Location: Codable, Hashable {
        var workspace: String? = nil
        var surface: String? = nil
        var session: String? = nil
        var pane: String? = nil
    }

    let launcher: String
    var location: Location? = nil
    var focusable: Bool? = nil

    var hostName: String? {
        switch launcher {
        case "cmux": return "cmux"
        case "tmux": return "tmux"
        default: return nil
        }
    }
}

struct GoalEditorState: Equatable {
    enum Mode: Equatable {
        case newGoal
        case editGoal
        case budget
    }

    private(set) var sessionID: String?
    private(set) var mode: Mode?

    mutating func begin(_ mode: Mode, sessionID: String) {
        self.sessionID = sessionID
        self.mode = mode
    }

    mutating func select(sessionID: String) {
        if self.sessionID != sessionID { reset() }
    }

    mutating func reset() {
        sessionID = nil
        mode = nil
    }

    func canCommit(currentSessionID: String?) -> Bool {
        mode != nil && sessionID != nil && sessionID == currentSessionID
    }
}

struct LauncherOption: Decodable, Identifiable, Hashable {
    let id: String
    let label: String
    let agents: [String]
    let available: Bool
    let detail: String?
    /// Whether the launcher applies every creation setting. A terminal launcher
    /// takes only the folder, message, model and name.
    let sessionOptions: Bool

    enum CodingKeys: String, CodingKey { case id, label, agents, available, detail, sessionOptions }

    init(id: String, label: String? = nil, agents: [String] = [], available: Bool = true, detail: String? = nil, sessionOptions: Bool = false) {
        self.id = id
        self.label = label ?? id
        self.agents = agents
        self.available = available
        self.detail = detail
        self.sessionOptions = sessionOptions
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        label = try container.decode(String.self, forKey: .label)
        agents = try container.decodeIfPresent([String].self, forKey: .agents) ?? []
        available = try container.decode(Bool.self, forKey: .available)
        detail = try container.decodeIfPresent(String.self, forKey: .detail)
        sessionOptions = try container.decodeIfPresent(Bool.self, forKey: .sessionOptions) ?? false
    }

    var isTerminal: Bool { !sessionOptions }
}

struct ProjectIdentity: Codable, Hashable {
    let id: String
    let displayName: String
    let rootPath: String
    let kind: String
    var archived: Bool? = nil
    var added: Bool? = nil
}

struct CheckoutIdentity: Codable, Hashable {
    let id: String?
    let path: String?
    let branch: String?
    let detachedHead: String?
    let isMain: Bool?
    let dirty: Bool?
}

struct QueueState: Decodable, Identifiable, Equatable {
	let operation: String?
    var sourceSessionId: String?
    let effort: String?
    let mode: String?
    let serviceTier: String?
    let outputSchema: RecordedJSON?
    let id: Int64
    var sessionId: String
    let target: String
    let message: String
    let model: String?
    let status: String
    let attempts: Int
    let lastError: String?
    let queuedAt: String
    var heldBy: String? = nil
    let historical: Bool?
    let evidence: String
    var hostID: String? = nil

    var listID: String { "\(hostID ?? "")/\(id)" }

    var isHistorical: Bool {
        historical == true || status == "delivered" || status == "canceled"
    }

    var queuedDate: Date? { TimestampParser.date(queuedAt) }

    /// The send receipt for a queued message, worded as the daemon words it.
    var queuedReceipt: String {
        heldBy == "waiting"
            ? "Queued for \(target), which is waiting on you; sends after you answer."
            : "Queued for \(target); sends when current turn ends."
    }

    /// An unconfirmed live delivery is a neutral receipt, whatever retry state
    /// the queue holds it in.
    var outcomeLabel: String {
        switch (evidence, isHistorical) {
        case ("unknown", true): return "Never confirmed"
        case ("unknown", false): return "Submitted"
        case ("failed", true): return "Delivery failed"
        case ("transport_accepted", _): return "Transport accepted"
        default:
            switch status {
            case "dead": return "Delivery needs review"
            case "inflight": return "Sending"
            default: return status.capitalized
            }
        }
    }

    /// Why a pending message has not been delivered yet.
    var holdLabel: String {
        switch heldBy {
        case "waiting": return "Held: this agent is waiting on you"
        case "busy": return "Held: this agent is working"
        case "offline": return "Held: this agent is offline"
        case "unknown": return "Held: this agent's state is unknown"
        case "refused": return "Held: this agent refused to start"
        case "retrying": return "Held: waiting to retry"
        case "behind": return "Held: behind an earlier message"
        case "notLoaded": return "Held: this conversation is not loaded"
        case "hostUnreachable": return "Held: this agent's host is unreachable"
        default: return "Delivers on the next scan"
        }
    }
}

indirect enum RecordedJSON: Codable, Equatable {
    case object([String: RecordedJSON]), array([RecordedJSON]), string(String), number(Double), bool(Bool), null
    init(from decoder: Decoder) throws {
        let value = try decoder.singleValueContainer()
        if value.decodeNil() { self = .null }
        else if let item = try? value.decode(Bool.self) { self = .bool(item) }
        else if let item = try? value.decode(String.self) { self = .string(item) }
        else if let item = try? value.decode(Double.self) { self = .number(item) }
        else if let item = try? value.decode([String: RecordedJSON].self) { self = .object(item) }
        else { self = .array(try value.decode([RecordedJSON].self)) }
    }
    func encode(to encoder: Encoder) throws {
        var value = encoder.singleValueContainer()
        switch self {
        case .object(let item): try value.encode(item)
        case .array(let item): try value.encode(item)
        case .string(let item): try value.encode(item)
        case .number(let item): try value.encode(item)
        case .bool(let item): try value.encode(item)
        case .null: try value.encodeNil()
        }
    }
    var formatted: String {
        let encoder = JSONEncoder(); encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        return (try? encoder.encode(self)).flatMap { String(data: $0, encoding: .utf8) } ?? ""
    }
}

struct AttentionState: Decodable, Identifiable, Equatable {
    let id: Int64
    let sessionId: String
    let target: String
    let queueId: Int64
    let reason: String
    let requestedAction: String
    let createdAt: String
}

struct DeliveryProblem: Decodable, Identifiable, Equatable {
    let deliveryId: Int64
    let sessionId: String
    let sourceSessionId: String?
    let attentionSessionId: String?
    let message: String
    let reason: String
    let status: String?
    let at: String
    /// The paired computer the problem came from, on a phone that merges
    /// several; delivery ids are only unique on one computer.
    var hostID: String? = nil

    var id: String { "\(hostID ?? "")/\(deliveryId)" }

    var reasonText: String {
        let words = reason.replacingOccurrences(of: "_", with: " ").trimmingCharacters(in: .whitespacesAndNewlines)
        guard let first = words.first else { return "The message was not delivered." }
        return first.uppercased() + words.dropFirst()
    }
}

struct ChannelState: Decodable, Identifiable, Equatable {
    var id: String { name }
    let name: String
    let members: [String]
    let memberDetails: [ChannelMemberState]?
}

struct ChannelMemberState: Decodable, Identifiable, Equatable {
    let id: String
    let display: String
}

struct RelayState: Decodable, Identifiable, Equatable {
    let handoff: String
    let pendingCount: Int64
    let lastError: String?
    let id: Int64
    let from: String
    let to: String
    let pattern: String
    let once: Bool
    let active: Bool
    let fireCount: Int64
    let lastFiredAt: String?
}

struct HistoryState: Decodable, Identifiable, Equatable {
    let id: Int64
    let createdAt: String
    let kind: String
    let sessionId: String?
    let sourceSessionId: String?
    let target: String?
    let source: String?
    let queueId: Int64?
    let message: String?
    let result: String?
    let error: String?
    var evidence: String? = nil
    var operation: String? = nil
}

struct DaemonState: Decodable, Equatable {
    let running: Bool
    let pid: Int?
    let stale: Bool?
    let refreshError: String?
    var host: SessionHost? = nil
    var hosts: [HostHealth]? = nil

    /// The paired host a host-qualified session id names, for a session the
    /// snapshot has not listed yet.
    func pairedHost(owning sessionID: String) -> SessionHost? {
        guard let range = sessionID.range(of: "::") else { return nil }
        let hostID = String(sessionID[..<range.lowerBound])
        return hosts?.first { $0.id == hostID }.map { SessionHost(id: $0.id, name: $0.name, state: $0.state) }
    }
}

struct DashboardSnapshot: Decodable {
    let updatedAt: String
    let eventCursor: UInt64?
    let hostEpoch: String?
    let catalogSeq: UInt64?
    let daemon: DaemonState
    var surfaces: [SurfaceState]
    var sessions: [SessionState]
    var totalSessions: Int
    var nextCursor: String? = nil
    let queue: [QueueState]
    let channels: [ChannelState]
    let relays: [RelayState]
    let history: [HistoryState]
    let attention: [AttentionState]
    var deliveryProblems: [DeliveryProblem]?
    let codexRecentHours: Int
    let busyDelivery: String?
    var rekeyed: [String: String]? = nil
    var signin: [SigninConsumerState]? = nil
    /// What this host's sessions call the person; an older host sends none.
    var person: PersonState? = nil
    /// Only a whole-catalog snapshot carries projects; a page leaves them out.
    var projects: [ProjectState]? = nil

    func hasSamePresentation(as other: DashboardSnapshot) -> Bool {
        daemon == other.daemon &&
            surfaces == other.surfaces &&
            sessions == other.sessions &&
            totalSessions == other.totalSessions &&
            queue == other.queue &&
            channels == other.channels &&
            relays == other.relays &&
            history == other.history &&
            attention == other.attention &&
            deliveryProblems == other.deliveryProblems &&
            codexRecentHours == other.codexRecentHours &&
            busyDelivery == other.busyDelivery &&
            signin == other.signin &&
            person == other.person &&
            projects == other.projects
    }

    /// Sessions, surfaces and delivery problems change only through catalog
    /// events, so a snapshot older than the events applied keeps the client's.
    func keepingCatalog(of current: DashboardSnapshot?, appliedThrough cursor: UInt64) -> DashboardSnapshot {
        guard let current, let catalogSeq, hostEpoch == current.hostEpoch, catalogSeq < cursor else { return self }
        var merged = self
        merged.sessions = current.sessions
        merged.totalSessions = current.totalSessions
        merged.surfaces = current.surfaces
        merged.deliveryProblems = current.deliveryProblems
        return merged
    }
}

struct ExchangeState: Decodable, Identifiable {
    let user: String
    let assistant: String
    let timestamp: String
    var origin: String?
    var sender: String?
    var id: String { timestamp + user + assistant }

    var promptLabel: String {
        switch origin ?? "" {
        case "", "person": return "You"
        case "peer": return sender.map { "From \($0)" } ?? "From an agent"
        case "task-notification": return "Task notification"
        case "auto-continuation": return "Continued"
        case "agenthail-notice": return "Agenthail"
        default: return origin ?? "You"
        }
    }
}

struct ContextState: Decodable {
    let usedTokens: Int64
    let contextWindow: Int64
    let cumulativeTokens: Int64?
    let compacting: Bool
    let compactionCount: Int
    let reclaimedTokens: Int64?
    let inputTokens: Int64?
    let cachedInputTokens: Int64?
    let outputTokens: Int64?
	let reasoningOutputTokens: Int64?
	let windowEstimated: Bool?
	let contextWindowSource: String?
	let updatedAt: String?
    let lastCompactedAt: String?

    var exceedsEstimatedWindow: Bool {
        windowEstimated == true && contextWindow > 0 && usedTokens > contextWindow
    }
	var fraction: Double? {
		guard contextWindow > 0, usedTokens <= contextWindow else { return nil }
		return Double(usedTokens) / Double(contextWindow)
	}
}

extension ContextState {
    enum Level { case normal, high, full }

    /// How full the window is, for the composer's ring: high from 80 percent,
    /// full from 95 or once an estimated window is overrun.
    var level: Level {
        guard let fraction else { return exceedsEstimatedWindow ? .full : .normal }
        return fraction >= 0.95 ? .full : fraction >= 0.8 ? .high : .normal
    }

    var spokenLabel: String {
        if compacting { return "Compacting context" }
        if let fraction { return "Context \(Int((fraction * 100).rounded())) percent used" }
        return "\(usedTokens.formatted(.number.notation(.compactName))) tokens of context used"
    }
}

struct ModelOption: Decodable, Identifiable {
    let id: String
    let displayName: String
    let description: String?
    let `default`: Bool?
    let allowsCustom: Bool?
    let supportedReasoningEfforts: [String]?
    let defaultReasoningEffort: String?
}

struct SessionMetadata: Decodable {
    let context: ContextState?
    let goal: GoalState?
    let model: String?
    var pendingModel: String? = nil
    let models: [ModelOption]?
    let claudeRuns: [ClaudeRunObservation]?
    let claudeSubagents: [ClaudeSubagentLink]?
    var claudeSubagentsTotal: Int? = nil
    let errors: [String: String]?
}

struct ClaudeRunObservation: Decodable, Identifiable, Equatable {
    let recordPath: String
    let jobId: String
    let sessionId: String?
    let resumeSessionId: String?
    let runType: String?
    let providerState: String?
    let createdAt: String?
    let updatedAt: String?

    var id: String { recordPath.isEmpty ? jobId : recordPath }
}

struct ClaudeSubagentLink: Decodable, Identifiable, Equatable {
    let parentSessionId: String
    let agentId: String
    var agentType: String? = nil
    var description: String? = nil
    var toolUseId: String? = nil
    var depth: Int? = nil
    var working: Bool? = nil
    var lastActive: String? = nil
    let transcriptPath: String

    var id: String { agentId + transcriptPath }
}

struct SessionDetail: Decodable {
    var session: RawSession
    let journalSeq: UInt64?
    var alias: String?
    var limitResume: LimitResumeState?
    let exchanges: [ExchangeState]
    var capabilities: Capabilities
    var readOnly: Bool
    var readOnlyReason: String
    var context: ContextState?
    var goal: GoalState?
    var model: String?
    var pendingModel: String?
    var models: [ModelOption]?
    var claudeRuns: [ClaudeRunObservation]?
    var claudeSubagents: [ClaudeSubagentLink]?
    var claudeSubagentsTotal: Int?
    var metadataErrors: [String: String]?
    var timeline: SessionTimeline?
    let readSource: String?
    let readError: String?
    let transcriptWarning: String?
    let transcriptTruncated: Bool?
    let transcriptOriginalBytes: Int?
    let transcriptReturnedBytes: Int?

    var waitingPlace: WaitingPlace? {
        session.waitingOn.map { WaitingPlace($0, runtimeKind: session.runtimeKind, lifecycle: readOnly ? SessionLifecycle() : capabilities.lifecycle) }
    }
    var limitLabel: String? { limitResume?.label ?? session.usageLimit?.label }

    var liveState: SessionLiveState {
        get {
            SessionLiveState(name: session.name, alias: alias, status: session.status, lastActive: session.lastActive, cwd: session.cwd, transport: session.transport,
                             runtime: session.runtime, runtimeKind: session.runtimeKind, capabilities: capabilities, readOnly: readOnly, readOnlyReason: readOnlyReason,
                             waitingOn: session.waitingOn, backgroundTasks: session.backgroundTasks, usageLimit: session.usageLimit, limitResume: limitResume, activity: session.activity)
        }
        set {
            (session.name, alias, session.status, session.lastActive, session.cwd, session.transport) = (newValue.name, newValue.alias, newValue.status, newValue.lastActive, newValue.cwd, newValue.transport)
            (session.runtime, session.runtimeKind, capabilities, readOnly, readOnlyReason) = (newValue.runtime, newValue.runtimeKind, newValue.capabilities, newValue.readOnly, newValue.readOnlyReason)
            (session.waitingOn, session.backgroundTasks, session.usageLimit, limitResume, session.activity) = (newValue.waitingOn, newValue.backgroundTasks, newValue.usageLimit, newValue.limitResume, newValue.activity)
        }
    }
}

/// What a session detail shares with the catalog snapshot and can change
/// while the session is open. The rest of the detail is its history.
struct SessionLiveState: Equatable {
    var name: String
    var alias: String?
    var status: String
    var lastActive: String
    var cwd: String?
    var transport: String?
    var runtime: SessionRuntime?
    var runtimeKind: String?
    var capabilities: Capabilities
    var readOnly: Bool
    var readOnlyReason: String
    var waitingOn: WaitingState?
    var backgroundTasks: BackgroundTasks?
    var usageLimit: UsageLimitState?
    var limitResume: LimitResumeState?
    var activity: SessionActivity?

    init(name: String, alias: String?, status: String, lastActive: String, cwd: String?, transport: String?, runtime: SessionRuntime?, runtimeKind: String?,
         capabilities: Capabilities, readOnly: Bool, readOnlyReason: String, waitingOn: WaitingState?, backgroundTasks: BackgroundTasks?, usageLimit: UsageLimitState?,
         limitResume: LimitResumeState?, activity: SessionActivity? = nil) {
        (self.name, self.alias, self.status, self.lastActive, self.cwd, self.transport, self.runtime) = (name, alias, status, lastActive, cwd, transport, runtime)
        (self.runtimeKind, self.capabilities, self.readOnly, self.readOnlyReason) = (runtimeKind, capabilities, readOnly, readOnlyReason)
        (self.waitingOn, self.backgroundTasks, self.usageLimit, self.limitResume, self.activity) = (waitingOn, backgroundTasks, usageLimit, limitResume, activity)
    }

    /// The snapshot's state. A snapshot without lastActive keeps the one loaded.
    init(_ live: SessionState, lastActive loaded: String) {
        self.init(name: live.name, alias: live.alias, status: live.status, lastActive: live.lastActive ?? loaded, cwd: live.cwd, transport: live.transport,
                  runtime: live.runtime, runtimeKind: live.runtimeKind, capabilities: live.capabilities, readOnly: live.isReadOnly, readOnlyReason: live.readOnlyReason ?? "",
                  waitingOn: live.waitingOn, backgroundTasks: live.backgroundTasks, usageLimit: live.usageLimit, limitResume: live.limitResume, activity: live.activity)
    }
}

struct RawSession: Decodable {
    var id: String
    let surface: String
    var name: String
    var status: String
    var lastActive: String
    let source: String?
    var transport: String?
    var cwd: String?
    var pendingModel: String? = nil
    var runtime: SessionRuntime?
    var waitingOn: WaitingState? = nil
    var backgroundTasks: BackgroundTasks? = nil
    var usageLimit: UsageLimitState? = nil
    var activity: SessionActivity? = nil
    var runtimeKind: String? = nil

    /// "Working", with how long the turn has been quiet once it has been.
    func workingLabel(now: Date) -> String {
        activity?.quietNote(now: now).map { "Working · " + $0 } ?? "Working"
    }
}

struct GoalState: Decodable {
    let objective: String
    let status: String
    let timeUsedSeconds: Int?
    let tokensUsed: Int?
    let tokenBudget: Int?
    let createdAt: String?
    let updatedAt: String?
    let reason: String?

    var displayStatus: String {
        switch status {
        case "active": return "Active"
        case "paused": return "Paused"
        case "blocked", "usageLimited", "budgetLimited": return "Needs you"
        case "complete": return "Complete"
        default: return status.capitalized
        }
    }

    var needsAttention: Bool {
        ["blocked", "usageLimited", "budgetLimited"].contains(status)
    }

    enum CodingKeys: String, CodingKey {
        case objective, status, timeUsedSeconds, tokensUsed, tokenBudget, createdAt, updatedAt, reason
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        objective = try container.decode(String.self, forKey: .objective)
        status = try container.decode(String.self, forKey: .status)
        timeUsedSeconds = try container.decodeIfPresent(Int.self, forKey: .timeUsedSeconds)
        tokensUsed = try container.decodeIfPresent(Int.self, forKey: .tokensUsed)
        tokenBudget = try container.decodeIfPresent(Int.self, forKey: .tokenBudget)
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
        updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt)
        reason = try container.decodeIfPresent(String.self, forKey: .reason).flatMap { $0.isEmpty ? nil : $0 }
    }
}

struct DeviceState: Codable, Identifiable {
    let id: String
    let name: String
    let scopes: [String]
    let createdAt: String
    let lastSeenAt: String?
    let revokedAt: String?
    let pushEnabled: Bool
}

struct DeviceListResponse: Decodable {
    let devices: [DeviceState]
}

struct RemoteAccessState: Decodable {
    let enabled: Bool
    let desired: Bool
    let provider: String
    let url: String?
    let dnsName: String?
    let port: Int
    let error: String?
}

struct DashboardConfigState: Decodable {
    let listen: String
    let codexRecentHours: Int
    let busyDelivery: String
    var resumeAfterLimit: Bool? = nil
}

/// `set` is false while the name is the host account's own.
struct PersonState: Decodable, Equatable {
    let name: String
    let set: Bool
}

/// The person-name field: it shows the name the person set, and saves only a
/// non-empty change, so clearing it never resets the name by accident.
enum PersonNameDraft {
    static func field(for person: PersonState) -> String { person.set ? person.name : "" }

    static func submission(_ draft: String, current: PersonState) -> String? {
        let name = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !(current.set && name == current.name) else { return nil }
        return name
    }
}

/// Whether each Claude config folder starts Remote Control in new sessions.
/// `atStartup` is nil when the folder leaves it to Claude's default.
struct ClaudeRemoteControlState: Decodable, Equatable {
    struct Root: Decodable, Equatable, Identifiable {
        let root: String
        let name: String
        let atStartup: Bool?
        let problem: String?
        var id: String { root }
    }

    let available: Bool
    let roots: [Root]
}

struct DashboardSettingsState: Decodable {
    let dashboard: DashboardConfigState
    let remoteAccess: RemoteAccessState
    let notifications: NotificationStatusState
}

struct NotificationStatusState: Decodable {
    let enabled: Bool
    let available: Bool
    let authorization: String
    let authorized: Bool
    let alerts: Bool
    let sounds: Bool
    let error: String?
}

struct HistoryPageResponse: Decodable {
    let items: [HistoryState]
    let hasMore: Bool
    let nextBefore: Int64
    let kinds: [String]
}

struct SigninProfileState: Codable, Identifiable, Equatable {
    let id: String
    let label: String
    let browser: String
    let profile: String
    let createdAt: String
}

struct SigninConsumerState: Codable, Identifiable, Equatable {
    let id: String
    let label: String
    let purpose: String
    let profileId: String?
    let mode: String
    let browser: String?
    let browserLabel: String?
    let profile: String?
    let profileName: String?
    let problem: String?
    let unavailable: String?

    var choice: String {
        switch mode {
        case "off": return "none"
        case "profile": return profileId ?? "auto"
        default: return "auto"
        }
    }

    var resolvedLabel: String {
        if mode == "off" { return "Off. Reads no cookies." }
        guard let browserLabel, !browserLabel.isEmpty else { return "Uses the system default browser." }
        let profile = profile ?? ""
        if mode == "profile" { return "\(profileName ?? profile): \(browserLabel) · \(profile)." }
        let named = profileName.map { $0 == profile ? "" : " (\($0))" } ?? ""
        let place = browser == "safari" ? "Safari" : "\(browserLabel) · \(profile)\(named)"
        return "Automatic: \(place), the system default browser."
    }
}

struct SigninDetectedProfile: Codable, Identifiable, Equatable {
    let profile: String
    let name: String
    let profileId: String?

    var id: String { profile }
    var choiceLabel: String { name == profile ? name : "\(name) (\(profile))" }
}

struct SigninBrowserState: Codable, Identifiable, Equatable {
    let browser: String
    let label: String
    let available: Bool
    let detail: String?
    let profiles: [SigninDetectedProfile]

    var id: String { browser }
    var addableProfiles: [SigninDetectedProfile] { profiles.filter { $0.profileId == nil } }
}

/// The daemon's macOS privacy grants, which only the daemon can read.
struct PermissionsState: Codable, Equatable {
    let supported: Bool
    let holder: String?
    let grants: [PermissionGrant]
    let problem: String?

    var needsAttention: Bool { problem != nil || grants.contains { $0.state != "granted" } }
}

struct PermissionGrant: Codable, Equatable, Identifiable {
    let name: String
    let target: String?
    let targetName: String?
    let state: String
    let pane: String
    let url: String

    var id: String { name + (target ?? "") }
    var title: String { targetName.map { "\(name) of \($0)" } ?? name }
    var stateLabel: String {
        switch state {
        case "granted": "Granted"
        case "missing": "Missing"
        case "denied": "Denied"
        case "target_not_running": "Not checked: \(targetName ?? "the app") is not running"
        default: "Unknown"
        }
    }
}

struct PermissionsRequestResponse: Codable {
    let asked: Bool
}

struct SigninState: Codable, Equatable {
    let profiles: [SigninProfileState]
    let consumers: [SigninConsumerState]
    let browsers: [SigninBrowserState]

    func browserLabel(_ browser: String) -> String {
        browsers.first { $0.browser == browser }?.label ?? browser
    }

    var addableBrowsers: [SigninBrowserState] { browsers.filter { $0.available && !$0.addableProfiles.isEmpty } }
    var unavailableBrowsers: [SigninBrowserState] { browsers.filter { !$0.available } }
}

struct SigninProfileResponse: Decodable {
    let profile: SigninProfileState?
}

struct LoginLinkResponse: Decodable {
    let url: String
    let expiresAt: String
}

struct PairingResponse: Decodable {
    let id: String
    let expiresAt: String
    let pairingURL: String
    let endpoint: String
    let secret: String
    let scopes: [String]
}

struct PairedDeviceResponse: Decodable {
    struct Host: Decodable {
        let id: String
        let name: String
    }
    let device: DeviceState
    let token: String
    let `protocol`: Int
    let host: Host
}

struct AgenthailEvent: Decodable {
    let id: UInt64
    let type: String
    let timestamp: String
    let entityId: String?
    let snapshot: SnapshotRequest?

    private enum CodingKeys: String, CodingKey {
        case id, type, timestamp, entityId, data
    }

    init(id: UInt64, type: String, timestamp: String, entityId: String?, snapshot: SnapshotRequest? = nil) {
        self.id = id
        self.type = type
        self.timestamp = timestamp
        self.entityId = entityId
        self.snapshot = snapshot
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(UInt64.self, forKey: .id)
        type = try container.decode(String.self, forKey: .type)
        timestamp = try container.decode(String.self, forKey: .timestamp)
        entityId = try container.decodeIfPresent(String.self, forKey: .entityId)
        snapshot = type == "ui.snapshot.requested" ? try container.decodeIfPresent(SnapshotRequest.self, forKey: .data) : nil
    }
}

struct SnapshotRequest: Decodable, Equatable {
    let id: String
    let session: String?
    let width: Int?
    let height: Int?
    let device: String?
    var rev: String? = nil
    let deadlineMs: Int64
}

struct SessionTimeline: Decodable {
    let nextBefore: Int64?
    var items: [TimelineItem]
    let source: String?
    let truncated: Bool
    var unavailableReason: String?
}

struct SessionAttachment: Decodable, Equatable, Hashable {
    let id: String
    let mediaType: String
    let width: Int?
    let height: Int?
    let bytes: Int?

    var isImage: Bool { mediaType.hasPrefix("image/") }
}

// A file sent with a message that the agent received as a stored path. The
// path is on the host that owns the session.
struct AttachedFile: Decodable, Equatable, Hashable {
    let name: String
    var path = ""
    var bytes: Int64? = nil

    var sizeLabel: String? { bytes.map { ByteCountFormatter.string(fromByteCount: $0, countStyle: .file) } }
}

struct SessionStreamItem: Decodable {
    let itemId: String
    let version: UInt64
    let op: String
    let kind: String
    let turnId: String?
    let callId: String?
    let ts: String
    let body: String?
    let context: ContextState?
    let goal: GoalState?
    let role: String?
    let sender: String?
    let title: String?
    let status: String?
    var resetsAt: String? = nil
    let truncated: Bool
    let truncationReason: String?
    let bodyRef: String?
    let reason: String?
    let attachment: SessionAttachment?
    let files: [AttachedFile]?
    var commands: [RuntimeCommand]? = nil
    var unavailable: String? = nil
}

/// A command the session's runtime runs, as the composer offers it. `insert`
/// is the text it puts in the prompt, so clients never branch on the runtime.
struct RuntimeCommand: Decodable, Equatable, Sendable {
    var name: String
    var description: String
    var argumentHint: String
    var insert: String
}

/// The runtime commands a session can run now; `unavailable` says why there
/// are none when its route cannot run them.
struct RuntimeCommands: Decodable, Equatable, Sendable {
    var commands: [RuntimeCommand]
    var unavailable: String
}

struct SessionStreamBody: Decodable {
    let sessionId: String
    let bodyRef: String
    let start: Int
    let end: Int
    let total: Int
    let body: String
    let truncated: Bool
}

struct RetainedBodyResult {
    let text: String
    let error: String?
}

struct SessionStreamEvent: Decodable {
    let stream: String
    let sessionId: String
    let seq: UInt64
    let type: String
    let data: SessionStreamItem
    /// The row as a session page reads it; absent for rows a page leaves out.
    var item: TimelineItem? = nil
}

struct CatalogStreamEvent: Decodable {
    let stream: String
    let seq: UInt64
    let type: String
    let data: CatalogStreamData
}

struct CatalogStreamData: Decodable {
    let session: SessionState?
    let sessionId: String?
    let surface: String?
    let health: String?
    let detail: String?
    let runtime: SurfaceRuntime?
    let deliveryId: Int64?
    let queueCount: Int?
    let sourceSessionId: String?
    let attentionSessionId: String?
    let message: String?
    let reason: String?
    let status: String?
    let at: String?
    let rekeyedTo: String?
}

struct TimelineItem: Decodable, Identifiable, Equatable {
    let id: String
    let kind: String
    let role: String?
    let title: String
    let text: String
    let timestamp: String?
    let callId: String?
    let status: String?
    let truncated: Bool
    let truncationReason: String?
    let bodyRef: String?
    var attachment: SessionAttachment? = nil
    var sender: String? = nil
    var files: [AttachedFile]? = nil
    var resetsAt: String? = nil
    var turnId: String? = nil
    var agent: AgentMessage? = nil

    /// Model input a provider injected, such as project instructions or hook
    /// context, rather than conversation.
    var isInstructions: Bool {
        kind == "instructions" || ((kind == "message" || kind == "text") && (role == "developer" || role == "system"))
    }

    /// A notice's text with the time its usage limit resets, when it reports one.
    var noticeText: String {
        let text = self.text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let resetsAt, let date = TimestampParser.date(resetsAt) else { return text }
        return "\(text) Resets \(date.formatted(date: .abbreviated, time: .shortened))."
    }
}

struct SessionSearchResponse: Decodable {
    let results: [SessionSearchItem]
    let remoteError: String?
}
struct SessionSearchItem: Decodable, Identifiable {
    var id: String { session.id }
    var session: SessionState
    let snippet: String?
}

struct UploadedAttachment: Decodable, Equatable {
    let id: String
    let name: String
    let mediaType: String
    let bytes: Int64
}

/// A tool approval, questions, or an MCP server's request for input that an
/// open turn waits on a person for.
struct SessionRequest: Decodable, Hashable, Identifiable {
    struct Option: Decodable, Hashable {
        let label: String
        let description: String
    }

    struct Question: Decodable, Hashable {
        let question: String
        let header: String
        let multiSelect: Bool
        let options: [Option]
    }

    let id: String
    let kind: String
    let tool: String
    var title: String? = nil
    var displayName: String? = nil
    var description: String? = nil
    var reason: String? = nil
    var blockedPath: String? = nil
    var questions: [Question]? = nil
    let alwaysAllow: Bool
    var input: String? = nil
    var elicitation: Elicitation? = nil

    var isQuestion: Bool { kind == "question" }
    var summary: String { [title, displayName].compactMap { $0 }.first { !$0.isEmpty } ?? tool }

    private enum CodingKeys: String, CodingKey {
        case id, kind, tool, title, displayName, description, reason, blockedPath, questions, alwaysAllow, input, elicitation
    }

    init(id: String, kind: String, tool: String, title: String? = nil, questions: [Question]? = nil, alwaysAllow: Bool = false, input: String? = nil) {
        (self.id, self.kind, self.tool, self.title, self.questions, self.alwaysAllow, self.input) = (id, kind, tool, title, questions, alwaysAllow, input)
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        kind = try container.decode(String.self, forKey: .kind)
        tool = try container.decode(String.self, forKey: .tool)
        title = try container.decodeIfPresent(String.self, forKey: .title)
        displayName = try container.decodeIfPresent(String.self, forKey: .displayName)
        description = try container.decodeIfPresent(String.self, forKey: .description)
        reason = try container.decodeIfPresent(String.self, forKey: .reason)
        blockedPath = try container.decodeIfPresent(String.self, forKey: .blockedPath)
        questions = try container.decodeIfPresent([Question].self, forKey: .questions)
        elicitation = try container.decodeIfPresent(Elicitation.self, forKey: .elicitation)
        alwaysAllow = try container.decodeIfPresent(Bool.self, forKey: .alwaysAllow) ?? false
        if let raw = try container.decodeIfPresent(RequestInput.self, forKey: .input), let data = try? JSONEncoder().encode(raw) {
            input = String(data: data, encoding: .utf8)
        }
    }
}

/// An MCP server's request for input: a form of flat fields, or in url mode a
/// link for the person to open.
struct Elicitation: Decodable, Hashable {
    struct Option: Decodable, Hashable {
        let value: String
        let label: String
    }

    /// One form field. `type` is string, number, integer or boolean; a string
    /// with options takes one of their values, or with `multiSelect` a list.
    struct Field: Decodable, Hashable {
        let name: String
        let title: String
        let description: String
        let type: String
        let required: Bool
        let multiSelect: Bool
        var options: [Option]? = nil
        var initial: ElicitationValue? = nil

        var label: String { title.isEmpty ? name : title }

        private enum CodingKeys: String, CodingKey {
            case name, title, description, type, required, multiSelect, options
            case initial = "default"
        }

        init(name: String, title: String = "", description: String = "", type: String = "string", required: Bool = false, multiSelect: Bool = false, options: [Option]? = nil, initial: ElicitationValue? = nil) {
            (self.name, self.title, self.description, self.type, self.required, self.multiSelect, self.options, self.initial) = (name, title, description, type, required, multiSelect, options, initial)
        }

        init(from decoder: Decoder) throws {
            let container = try decoder.container(keyedBy: CodingKeys.self)
            name = try container.decode(String.self, forKey: .name)
            title = try container.decodeIfPresent(String.self, forKey: .title) ?? ""
            description = try container.decodeIfPresent(String.self, forKey: .description) ?? ""
            type = try container.decodeIfPresent(String.self, forKey: .type) ?? "string"
            required = try container.decodeIfPresent(Bool.self, forKey: .required) ?? false
            multiSelect = try container.decodeIfPresent(Bool.self, forKey: .multiSelect) ?? false
            options = try container.decodeIfPresent([Option].self, forKey: .options)
            initial = try? container.decodeIfPresent(ElicitationValue.self, forKey: .initial)
        }
    }

    let server: String
    let message: String
    let mode: String
    var url: String? = nil
    var fields: [Field]? = nil
}

/// A value an MCP form field takes.
enum ElicitationValue: Codable, Hashable, Sendable {
    case string(String), number(Double), bool(Bool), strings([String])

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let value = try? container.decode(Bool.self) { self = .bool(value) }
        else if let value = try? container.decode(Double.self) { self = .number(value) }
        else if let value = try? container.decode(String.self) { self = .string(value) }
        else { self = .strings(try container.decode([String].self)) }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value): try container.encode(value)
        case .number(let value): try container.encode(value)
        case .bool(let value): try container.encode(value)
        case .strings(let value): try container.encode(value)
        }
    }
}

/// A person's answer to a request: a notification button's `choice`, or a
/// `behavior` with an optional `always`, `message`, `answers`, or an MCP
/// form's `content`.
struct RequestDecision: Encodable, Hashable, Sendable {
    var choice: String? = nil
    var behavior: String? = nil
    var always: Bool? = nil
    var message: String? = nil
    var answers: [String: String]? = nil
    var content: [String: ElicitationValue]? = nil

    static let allow = RequestDecision(behavior: "allow")
    static let alwaysAllow = RequestDecision(behavior: "allow", always: true)
    static let deny = RequestDecision(behavior: "deny")
    static func answers(_ answers: [String: String]) -> RequestDecision { RequestDecision(behavior: "allow", answers: answers) }
    static func choice(_ choice: String) -> RequestDecision { RequestDecision(choice: choice) }
    static func content(_ content: [String: ElicitationValue]) -> RequestDecision { RequestDecision(behavior: "allow", content: content) }
}

/// A session's open requests, oldest first, and the permission mode it runs
/// in, empty when it runs in its settings' default.
struct SessionRequests: Decodable, Hashable {
    let permissionMode: String
    let requests: [SessionRequest]

    /// The requests without one the daemon called already answered or
    /// expired, so its card goes at once rather than staying clickable.
    func removing(_ id: String) -> SessionRequests {
        SessionRequests(permissionMode: permissionMode, requests: requests.filter { $0.id != id })
    }

    /// The modes a person can change a running session into; an empty mode is
    /// the settings' default, which a session can start in but not be set to.
    static let permissionModes = ["acceptEdits", "auto", "plan", "manual", "dontAsk", "bypassPermissions"]

    // Bypass permissions and Don't ask stay menu-only, so a key press never lands a session there.
    static let cyclingModes = ["manual", "acceptEdits", "plan", "auto"]

    static func cycled(from mode: String) -> String {
        guard let index = cyclingModes.firstIndex(of: mode) else { return mode == "" || mode == "default" ? "acceptEdits" : "manual" }
        return cyclingModes[(index + 1) % cyclingModes.count]
    }

    static func permissionModeLabel(_ mode: String) -> String {
        switch mode {
        case "": return "Default (Claude settings)"
        case "acceptEdits": return "Accept edits"
        case "auto": return "Auto"
        case "plan": return "Plan"
        case "manual": return "Manual"
        case "dontAsk": return "Don't ask"
        case "bypassPermissions": return "Bypass permissions"
        default: return mode
        }
    }
}

/// A request's tool input, kept only to show it as JSON.
private indirect enum RequestInput: Codable {
    case string(String), number(Double), bool(Bool), null
    case array([RequestInput]), object([String: RequestInput])

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() { self = .null }
        else if let value = try? container.decode(Bool.self) { self = .bool(value) }
        else if let value = try? container.decode(Double.self) { self = .number(value) }
        else if let value = try? container.decode(String.self) { self = .string(value) }
        else if let value = try? container.decode([RequestInput].self) { self = .array(value) }
        else { self = .object(try container.decode([String: RequestInput].self)) }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value): try container.encode(value)
        case .number(let value): try container.encode(value)
        case .bool(let value): try container.encode(value)
        case .null: try container.encodeNil()
        case .array(let value): try container.encode(value)
        case .object(let value): try container.encode(value)
        }
    }
}

extension SessionDetail {
    /// The model the conversation takes with its next turn sent through
    /// Agenthail, when that differs from the model it runs now.
    var nextTurnModel: String? {
        guard let pending = pendingModel ?? session.pendingModel, !pending.isEmpty, pending != model else { return nil }
        return pending
    }

    var modelLabel: String? {
        guard let pending = nextTurnModel else { return model }
        guard let model else { return "\(pending) from next turn" }
        return "\(model) · \(pending) from next turn"
    }
}

enum MetadataLabels {
    static func modelMenuTitle(current: String?, next: String?) -> String {
        guard let next, next != current else { return current ?? "Choose" }
        guard let current else { return "\(next) from next turn" }
        return "\(current) · \(next) from next turn"
    }
}

enum QueueTakeBack {
    /// Queued messages go back ahead of the draft, oldest first, as they would have been sent.
    static func text(queued: [String], draft: String) -> String {
        let parts = queued.filter { !$0.isEmpty } + (draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? [] : [draft])
        return parts.joined(separator: "\n\n")
    }
}

struct ComposerStash: Equatable {
    var text: String
    var attachments: [ComposerAttachment]
}

struct PromptItem: Decodable, Equatable, Identifiable {
    var id: String { sessionId + "/" + itemId }
    var sessionId: String
    let itemId: String
    let turnId: String?
    let ts: String
    let text: String

    var sentAt: Date? {
        TimestampParser.date(ts)
    }
}

struct PromptsPage: Decodable {
    let prompts: [PromptItem]
    let nextBefore: String?
}

/// Steps through what a session's composer has sent, newest first, with the
/// draft in progress kept aside until the person steps back past the newest.
struct PromptHistory: Equatable {
    private(set) var entries: [String] = []
    private(set) var position: Int?
    private var draft = ""

    var isBrowsing: Bool { position != nil }

    mutating func replace(_ texts: [String]) {
        var seen = Set<String>()
        entries = texts.filter { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && seen.insert($0).inserted }
        if let position, position >= entries.count { self.position = entries.isEmpty ? nil : entries.count - 1 }
    }

    mutating func older(from current: String) -> String? {
        let next = (position ?? -1) + 1
        guard next < entries.count else { return nil }
        if position == nil { draft = current }
        position = next
        return entries[next]
    }

    mutating func newer() -> String? {
        guard let position else { return nil }
        if position == 0 {
            self.position = nil
            return draft
        }
        self.position = position - 1
        return entries[position - 1]
    }

    mutating func stopBrowsing() {
        position = nil
        draft = ""
    }
}
