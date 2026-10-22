import Combine
import Foundation

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
    struct Usage: Decodable, Hashable {
        struct Limit: Decodable, Hashable {
            let kind: String
            let percent: Double
            var resetsAt: String? = nil
        }
        var limits: [Limit]? = nil
    }
    let name: String
    let root: String
    let primary: Bool
    var signedInAs: Login? = nil
    var usage: Usage? = nil
    var id: String { root }
    var choice: String { primary ? "" : root }
    var signedIn: Bool { signedInAs != nil }

    func exhaustedUntil(now: Date = Date()) -> Date? {
        let parser = ISO8601DateFormatter()
        parser.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter()
        let resets = (usage?.limits ?? []).filter { $0.percent >= 100 }.compactMap { limit in
            limit.resetsAt.flatMap { parser.date(from: $0) ?? plain.date(from: $0) }
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

struct SessionAdoptReply: Decodable {
    let ok: Bool
    var result: SessionAdoptResult? = nil
    var error: String? = nil
    var busy: String? = nil
    var needsConsent: Bool? = nil
    var unknown: Bool? = nil
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
struct ClaudeRootUsage: Decodable {
    let root: String
    var usage: ClaudeConfigRootOption.Usage? = nil
    var usageProblem: String? = nil
}

struct SessionCreationOptions: Decodable {
    struct Surface: Decodable, Identifiable { let id: String; let workspace: Bool; var connected: Bool? = nil }
    var host: SessionHost? = nil
    let surfaces: [Surface]
    let projects: [LaunchProject]
    var launchers: [LauncherOption]? = nil
    var claudeConfigRoots: [ClaudeConfigRootOption]? = nil
}

/// A project a new session can start in, most recently active first. The
/// root path is where the session starts.
struct LaunchProject: Decodable, Hashable, Identifiable {
    let id: String
    let displayName: String
    let rootPath: String
    let kind: String
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

    mutating func update(daemon: DaemonState?) {
        let paired = daemon?.hosts ?? []
        if let local = daemon?.host, !paired.isEmpty {
            hosts = [CreationHost(id: "", name: local.name, state: "connected")] + paired.map { CreationHost(id: $0.id, name: $0.name, state: $0.state) }
        } else {
            hosts = []
        }
        if !selected.isEmpty && selectedHost == nil {
            selected = ""
            options = nil
            models = [:]
            loadError = nil
            generation += 1
        }
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
    let ok: Bool
    let unknown: Bool?
    let status: String?
    let accepted: Bool?
    let retryable: Bool?
    let warning: String?
    let session: RawSession?
    let sessionId: String?
    let error: String?
    let launcher: String?
    let location: SessionRuntime.Location?
    var id: String? { session?.id ?? sessionId }
}

struct APIVersion: Decodable {
    let protocolVersion: Int
    let minimumProtocol: Int
    let maximumProtocol: Int
    let pushRelayUrl: String?

    enum CodingKeys: String, CodingKey {
        case protocolVersion = "protocol"
        case minimumProtocol
        case maximumProtocol
        case pushRelayUrl
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
    var lifecycle = SessionLifecycle()

    init(send: Bool = false, stream: Bool = false, reply: Bool = false, goal: Bool = false, goalSet: Bool = false, compact: Bool = false, model: Bool = false, interrupt: Bool = false, steer: Bool = false, attach: Bool = false, commands: Bool = false, respond: Bool = false, permissionMode: Bool = false, lifecycle: SessionLifecycle = SessionLifecycle()) {
        (self.send, self.stream, self.reply, self.goal, self.goalSet, self.compact, self.model, self.interrupt, self.steer, self.attach, self.commands, self.respond, self.permissionMode, self.lifecycle) = (send, stream, reply, goal, goalSet, compact, model, interrupt, steer, attach, commands, respond, permissionMode, lifecycle)
    }

    // A paired host on an older release omits goalSet, attach, commands,
    // respond, permissionMode and lifecycle; it cannot do any of them.
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

    var needsAttention: Bool { health != "healthy" || !(runtime?.advice.isEmpty ?? true) }
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
    var holders: [SessionHolder]? = nil
    var claudeConfigRoot: SessionConfigRoot? = nil
    var runtimeKind: String? = nil
    /// A session its daemon can open in Agenthail's host.
    var canAdopt: Bool { capabilities.lifecycle.adopt && !isReadOnly }

    var displayName: String {
        if let alias, !alias.isEmpty { return "@\(alias)" }
        return name.isEmpty ? id : name
    }

    var isWorking: Bool { status == "busy" }
    var isWaitingOnYou: Bool { waitingOn != nil }
    /// Between turns with background tasks still running: not finished yet.
    var isWaitingOnBackground: Bool { !isWorking && backgroundTasks?.running == true }
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
struct WaitingState: Codable, Hashable {
    let kind: String
    let id: String
    var since: String? = nil
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
    let subagentCount: Int
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
                subagentCount: counted.count + observed.count,
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
        let noun = family.subagentCount == 1 ? "subagent" : "subagents"
        return family.workingSubagents > 0 ? "\(family.subagentCount) \(noun), \(family.workingSubagents) working" : "\(family.subagentCount) \(noun)"
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
    let sourceSessionId: String?
    let effort: String?
    let mode: String?
    let serviceTier: String?
    let outputSchema: RecordedJSON?
    let id: Int64
    let sessionId: String
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

    var isHistorical: Bool {
        historical == true || status == "delivered" || status == "canceled"
    }

    /// The queue stores queuedAt as SQLite's UTC "yyyy-MM-dd HH:mm:ss".
    var queuedDate: Date? { QueueState.queuedAtFormat.date(from: queuedAt) }

    private static let queuedAtFormat: DateFormatter = {
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = TimeZone(identifier: "UTC")
        formatter.dateFormat = "yyyy-MM-dd HH:mm:ss"
        return formatter
    }()

    /// The send receipt for a queued message, worded as the daemon words it.
    var queuedReceipt: String {
        heldBy == "waiting"
            ? "Queued for \(target), which is waiting on you; sends after you answer."
            : "Queued for \(target); sends when current turn ends."
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
            signin == other.signin
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
        case "": return "You"
        case "peer": return sender.map { "From \($0)" } ?? "From an agent"
        case "task-notification": return "Task notification"
        case "auto-continuation": return "Continued"
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
    let alias: String?
    let exchanges: [ExchangeState]
    let capabilities: Capabilities
    let readOnly: Bool
    let readOnlyReason: String
    var context: ContextState?
    var goal: GoalState?
    var model: String?
    var pendingModel: String?
    var models: [ModelOption]?
    var claudeRuns: [ClaudeRunObservation]?
    var claudeSubagents: [ClaudeSubagentLink]?
    var metadataErrors: [String: String]?
    var timeline: SessionTimeline?
    let readSource: String?
    let readError: String?
    let transcriptWarning: String?
    let transcriptTruncated: Bool?
    let transcriptOriginalBytes: Int?
    let transcriptReturnedBytes: Int?
}

struct RawSession: Decodable {
    let id: String
    let surface: String
    let name: String
    var status: String
    let lastActive: String
    let source: String?
    let transport: String?
    let cwd: String?
    var pendingModel: String? = nil
    let runtime: SessionRuntime?
    var waitingOn: WaitingState? = nil
    var backgroundTasks: BackgroundTasks? = nil
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

struct PairingResponse: Decodable {
    let id: String
    let expiresAt: String
    let pairingURL: String
    let endpoint: String
    let secret: String
    let scopes: [String]
}

struct PairedDeviceResponse: Decodable {
    let device: DeviceState
    let token: String
    let `protocol`: Int
}

struct AgenthailEvent: Decodable {
    let id: UInt64
    let type: String
    let timestamp: String
    let entityId: String?
}

struct SessionTimeline: Decodable {
    let nextBefore: Int64?
    var items: [TimelineItem]
    let source: String?
    let truncated: Bool
    let unavailableReason: String?
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

    var isPeerMessage: Bool { kind == "message" && role == "peer" }
    /// Model input a provider injected, such as project instructions or hook
    /// context, rather than conversation.
    var isInstructions: Bool {
        kind == "instructions" || ((kind == "message" || kind == "text") && (role == "developer" || role == "system"))
    }
    var peerSender: String { sender.flatMap { $0.isEmpty ? nil : $0 } ?? "another agent" }
    var peerLabel: String { "From \(peerSender)" }

    /// A notice's text with the time its usage limit resets, when it reports one.
    var noticeText: String {
        let text = self.text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let resetsAt, let date = ISO8601DateFormatter().date(from: resetsAt) else { return text }
        return "\(text) Resets \(date.formatted(date: .abbreviated, time: .shortened))."
    }
}

struct SessionSearchResponse: Decodable {
    let results: [SessionSearchItem]
    let remoteError: String?
}
struct SessionSearchItem: Decodable, Identifiable {
    var id: String { session.id }
    let session: SessionState
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
