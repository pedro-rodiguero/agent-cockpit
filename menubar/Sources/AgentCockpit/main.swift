// Agent Cockpit menu bar app. Polls `cockpit json`, shows how many sessions need you, and posts a
// notification when a session starts waiting, looks stuck, or finishes a long turn, or when a new inbox
// item arrives. Sessions marked quiet (sources whose backend notifies on its own) only show in the menu.
import AppKit
import UserNotifications

// MARK: - Tuning

enum Tuning {
    /// How often `cockpit json` is run.
    static let pollInterval: TimeInterval = 3
    /// A busy → idle transition is only worth a notification when the turn ran at least this long.
    static let longTurn: TimeInterval = 60
    /// Longest notification body, in characters.
    static let notificationBodyLimit = 180
    /// Longest second line of a menu row, in characters.
    static let rowSubtitleLimit = 70
}

// MARK: - Model (mirrors internal/model/model.go)

/// What a session needs from the person, if anything. Values cockpit adds later decode as `.idle`, so
/// an older menu bar app keeps working against a newer binary.
enum SessionState: String, Decodable, CaseIterable {
    case waiting, asked, stuck, inbox, busy, idle, todo

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = SessionState(rawValue: raw) ?? .idle
    }

    /// Whether the session is blocked on the person.
    var needsYou: Bool { self == .waiting || self == .asked || self == .stuck || self == .inbox }

    var color: NSColor {
        switch self {
        case .waiting: return Palette.waiting
        case .asked: return Palette.asked
        case .stuck: return Palette.stuck
        case .inbox: return Palette.inbox
        case .busy: return Palette.busy
        case .idle, .todo: return .secondaryLabelColor
        }
    }

    var glyph: String {
        switch self {
        case .waiting: return "▲"
        case .asked: return "?"
        case .stuck: return "✕"
        case .inbox: return "✉"
        case .busy: return "●"
        case .idle: return "○"
        case .todo: return "□"
        }
    }
}

struct Session: Decodable {
    let id: String
    let agent: String
    let name: String
    let state: SessionState
    let detail: String?
    let title: String?
    let lastPrompt: String?
    let cwd: String?
    let since: Date?
    let lastActivity: Date?
    let quiet: Bool?

    /// Whether the session came from the named source. External sources prefix ids with `<source>:`
    /// and default `agent` to the source name; built-in sources set `agent` to their own name.
    func belongs(to source: String) -> Bool {
        agent == source || id.hasPrefix(source + ":")
    }
}

struct Snapshot: Decodable {
    let at: Date
    let sessions: [Session]?
    /// Source name → its last error, for sources that failed on this pass.
    let errors: [String: String]?
}

enum Palette {
    static let gold = NSColor(srgbRed: 0.753, green: 0.541, blue: 0.180, alpha: 1)
    static let waiting = NSColor(srgbRed: 0.851, green: 0.502, blue: 0.247, alpha: 1)
    static let asked = NSColor(srgbRed: 0.498, green: 0.682, blue: 0.690, alpha: 1)
    static let stuck = NSColor(srgbRed: 0.761, green: 0.353, blue: 0.263, alpha: 1)
    static let inbox = NSColor(srgbRed: 0.604, green: 0.659, blue: 0.471, alpha: 1)
    static let busy = NSColor(srgbRed: 0.788, green: 0.647, blue: 0.306, alpha: 1)
}

// MARK: - Transition rules (pure)

/// A notification the app should post for one session.
struct Alert {
    let session: Session
    let title: String
    let body: String
}

/// Decides which notifications one poll should post. Pure: the same inputs always give the same output.
///
/// - `waiting` and `stuck` notify on entering the state.
/// - `inbox` notifies only for an item not seen before.
/// - `idle` notifies after `busy` when the turn ran longer than `Tuning.longTurn`.
/// - Quiet sessions never notify.
///
/// - Parameters:
///   - previous: the sessions of the last applied poll, by id.
///   - current: the sessions of this poll.
///   - busySince: when each session was first seen busy, as of the last poll.
///   - now: the time of this poll.
func alerts(previous: [String: Session], current: [Session], busySince: [String: Date], now: Date) -> [Alert] {
    current.filter { !($0.quiet ?? false) }.compactMap { s in
        let before = previous[s.id]?.state
        switch s.state {
        case .waiting where before != .waiting:
            return Alert(session: s, title: "\(s.name) is waiting for you", body: s.detail ?? s.title ?? "")
        case .inbox where before == nil:
            return Alert(session: s, title: "\(s.name): \(s.detail ?? "new item")", body: s.title ?? "")
        case .stuck where before != .stuck:
            return Alert(session: s, title: "\(s.name) looks stuck", body: s.detail ?? "")
        case .idle where before == .busy:
            guard let start = busySince[s.id], now.timeIntervalSince(start) > Tuning.longTurn else { return nil }
            return Alert(session: s, title: "\(s.name) finished", body: s.title ?? s.lastPrompt ?? "")
        default:
            return nil
        }
    }
}

/// When each busy session in `current` started its turn: kept from `busySince` if it was already busy,
/// `now` if it just became busy. Sessions that are not busy, or are gone, drop out.
func trackBusy(_ current: [Session], busySince: [String: Date], now: Date) -> [String: Date] {
    var next: [String: Date] = [:]
    for s in current where s.state == .busy {
        next[s.id] = busySince[s.id] ?? now
    }
    return next
}

/// The sessions of `snapshot`, plus the previous sessions of every source that failed on this pass and
/// are missing from it. A source that fails briefly then neither drops its items nor re-announces them
/// as new when it recovers.
func sessionsCarryingOverFailedSources(_ snapshot: Snapshot, previous: [String: Session]) -> [Session] {
    let sessions = snapshot.sessions ?? []
    let failed = Array((snapshot.errors ?? [:]).keys)
    guard !failed.isEmpty else { return sessions }
    let present = Set(sessions.map(\.id))
    let carried = previous.values
        .filter { s in !present.contains(s.id) && failed.contains { s.belongs(to: $0) } }
        .sorted { $0.id < $1.id }
    return sessions + carried
}

// MARK: - Polling `cockpit json`

/// Why a poll produced no snapshot.
enum PollFailure: Error {
    case notFound(path: String)
    case failed(message: String)
    case undecodable

    var message: String {
        switch self {
        case .notFound(let path): return "cockpit not found at \((path as NSString).abbreviatingWithTildeInPath)"
        case .failed(let message): return "cockpit json failed: \(message)"
        case .undecodable: return "cockpit json returned unreadable output"
        }
    }
}

enum CockpitJSON {
    /// cockpit writes RFC 3339 times, with fractional seconds unless they are zero.
    private static let fractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
    private static let whole: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            return fractional.date(from: s) ?? whole.date(from: s) ?? .distantPast
        }
        return d
    }()

    /// Runs `<binary> json` and decodes its output. Blocks; call it off the main thread.
    static func fetch(binary: String) -> Result<Snapshot, PollFailure> {
        guard FileManager.default.isExecutableFile(atPath: binary) else { return .failure(.notFound(path: binary)) }
        let run: ProcessOutput
        do {
            run = try ProcessOutput.run(binary, arguments: ["json"])
        } catch {
            return .failure(.failed(message: error.localizedDescription))
        }
        guard run.status == 0 else {
            let line = run.stderr.split(whereSeparator: \.isNewline).first.map(String.init)
            return .failure(.failed(message: line ?? "exit status \(run.status)"))
        }
        guard let snapshot = try? decoder.decode(Snapshot.self, from: run.stdout) else { return .failure(.undecodable) }
        return .success(snapshot)
    }
}

/// The result of running a process to completion.
struct ProcessOutput {
    let status: Int32
    let stdout: Data
    let stderr: String

    /// Runs the program and waits for it. Both pipes are drained concurrently, so a chatty stderr cannot
    /// fill its buffer and stall the child while stdout is being read.
    static func run(_ path: String, arguments: [String]) throws -> ProcessOutput {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: path)
        p.arguments = arguments
        let out = Pipe(), err = Pipe()
        p.standardOutput = out
        p.standardError = err
        try p.run()
        var errData = Data()
        let drained = DispatchGroup()
        drained.enter()
        DispatchQueue.global(qos: .utility).async {
            errData = err.fileHandleForReading.readDataToEndOfFile()
            drained.leave()
        }
        let outData = out.fileHandleForReading.readDataToEndOfFile()
        drained.wait()
        p.waitUntilExit()
        return ProcessOutput(status: p.terminationStatus, stdout: outData, stderr: String(decoding: errData, as: UTF8.self))
    }
}

// MARK: - App

final class App: NSObject, NSApplicationDelegate, NSMenuDelegate, UNUserNotificationCenterDelegate {
    let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    let menu = NSMenu()
    /// The sessions shown, including those carried over from failed sources.
    var sessions: [Session] = []
    /// Set when the last poll failed; cleared by the next good one.
    var failure: PollFailure?
    var previous: [String: Session] = [:]
    var busySince: [String: Date] = [:]
    /// False until the first snapshot is applied, so sessions already running at launch do not notify.
    var primed = false
    /// True while a `cockpit json` is running; a tick that finds it set is skipped, so results are
    /// applied in the order they were requested.
    var polling = false
    var timer: Timer?
    /// The cockpit binary: $COCKPIT_BIN, then the path the installer recorded in Info.plist, then the
    /// usual install locations.
    let cockpit: String = {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let candidates = [
            ProcessInfo.processInfo.environment["COCKPIT_BIN"],
            Bundle.main.object(forInfoDictionaryKey: "CockpitBinary") as? String,
            "\(home)/.local/bin/cockpit",
            "/opt/homebrew/bin/cockpit",
            "/usr/local/bin/cockpit",
        ].compactMap { $0 }
        return candidates.first { FileManager.default.isExecutableFile(atPath: $0) } ?? candidates[0]
    }()
    let settings = CockpitSettings()
    lazy var dashboard = DashboardWindow(cockpit: cockpit, settings: settings)
    lazy var settingsWindow = SettingsWindow(settings: settings, cockpit: cockpit)
    /// Pending restart of the dashboard after a change to settings it reads only at startup.
    var restart: DispatchWorkItem?
    let hotKey = HotKey()

    func applicationDidFinishLaunching(_ n: Notification) {
        menu.delegate = self
        item.menu = menu
        NSApp.mainMenu = mainMenu()
        hotKey.action = { [weak self] in self?.toggleDashboard() }
        registerHotKey()
        NotificationCenter.default.addObserver(forName: CockpitSettings.changed, object: settings, queue: .main) { [weak self] n in
            self?.settingsChanged(n.userInfo?["key"] as? String ?? "")
        }
        render()
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        center.requestAuthorization(options: [.alert, .sound]) { _, _ in }
        poll()
        let timer = Timer(timeInterval: Tuning.pollInterval, repeats: true) { [weak self] _ in self?.poll() }
        RunLoop.main.add(timer, forMode: .common) // keep ticking while the menu is open
        self.timer = timer
        // The LaunchAgent starts the app at login with --background; opened any other way (Spotlight,
        // Finder, the Dock) it was asked for, so it shows the dashboard.
        if !CommandLine.arguments.contains("--background") {
            dashboard.show()
        }
    }

    func poll() {
        if settings.reload() { // edited by hand
            dashboard.apply()
            registerHotKey()
        }
        guard !polling else { return }
        polling = true
        let binary = cockpit
        DispatchQueue.global(qos: .utility).async {
            let result = CockpitJSON.fetch(binary: binary)
            DispatchQueue.main.async {
                self.polling = false
                switch result {
                case .success(let snapshot): self.apply(snapshot)
                case .failure(let failure): self.fail(failure)
                }
            }
        }
    }

    /// Applies a good snapshot: posts notifications for transitions, then records it as the new baseline.
    func apply(_ snapshot: Snapshot) {
        let now = Date()
        let current = sessionsCarryingOverFailedSources(snapshot, previous: previous)
        if primed && settings.notify {
            alerts(previous: previous, current: current, busySince: busySince, now: now).forEach(post)
        }
        busySince = trackBusy(current, busySince: busySince, now: now)
        previous = Dictionary(current.map { ($0.id, $0) }, uniquingKeysWith: { first, _ in first })
        sessions = current
        failure = nil
        primed = true
        render()
    }

    /// Shows a failed poll. The baseline is kept, so sessions are not re-announced when cockpit recovers.
    func fail(_ failure: PollFailure) {
        self.failure = failure
        sessions = []
        render()
    }

    func post(_ alert: Alert) {
        let s = alert.session
        let c = UNMutableNotificationContent()
        c.title = alert.title
        c.subtitle = "\(s.agent) · \(((s.cwd ?? "") as NSString).abbreviatingWithTildeInPath)"
        c.body = String(alert.body.prefix(Tuning.notificationBodyLimit))
        c.sound = .default
        c.userInfo = ["id": s.id, "inbox": s.state == .inbox]
        let request = UNNotificationRequest(identifier: "\(s.id)-\(s.state.rawValue)", content: c, trigger: nil)
        UNUserNotificationCenter.current().add(request)
    }

    func userNotificationCenter(_ c: UNUserNotificationCenter, didReceive r: UNNotificationResponse, withCompletionHandler done: @escaping () -> Void) {
        let info = r.notification.request.content.userInfo
        if let id = info["id"] as? String {
            // An inbox item opens its link; a session shows in the dashboard, selected.
            if info["inbox"] as? Bool ?? false { open(id) } else { showSession(id) }
        }
        done()
    }

    func userNotificationCenter(_ c: UNUserNotificationCenter, willPresent n: UNNotification, withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner, .sound])
    }

    // MARK: status item

    func render() {
        let font = NSFont.monospacedSystemFont(ofSize: 12, weight: .medium)
        let title = NSMutableAttributedString(string: "◆", attributes: [.foregroundColor: Palette.gold, .font: font])
        func append(_ text: String, _ color: NSColor) {
            title.append(NSAttributedString(string: " " + text, attributes: [.foregroundColor: color, .font: font]))
        }
        if failure != nil {
            append("!", Palette.stuck)
        }
        for state in SessionState.allCases where state.needsYou {
            let n = sessions.filter { $0.state == state }.count
            if n > 0 { append("\(state.glyph)\(n)", state.color) }
        }
        let busy = sessions.filter { $0.state == .busy }.count
        if busy > 0 { append("\(SessionState.busy.glyph)\(busy)", .secondaryLabelColor) }
        item.button?.attributedTitle = title
        item.button?.toolTip = failure?.message ?? "Agent Cockpit"
    }

    // MARK: menu

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        let groups: [(label: String, includes: (SessionState) -> Bool)] = [
            ("Needs you", { $0.needsYou }),
            ("Working", { $0 == .busy }),
            ("Idle", { $0 == .idle }),
            ("To do", { $0 == .todo }),
        ]
        if let failure = failure {
            menu.addItem(disabled(failure.message))
        } else if sessions.isEmpty {
            menu.addItem(disabled("No active sessions"))
        }
        for group in groups {
            let list = sessions.filter { group.includes($0.state) }
            if list.isEmpty { continue }
            if !menu.items.isEmpty { menu.addItem(.separator()) }
            menu.addItem(header(group.label, count: list.count))
            list.forEach { menu.addItem(row($0)) }
        }
        menu.addItem(.separator())
        let window = NSMenuItem(title: "Open Dashboard", action: #selector(openDashboard), keyEquivalent: "d")
        window.target = self
        menu.addItem(window)
        let panel = NSMenuItem(title: "Open Dashboard in Terminal", action: #selector(openPanel), keyEquivalent: "")
        panel.target = self
        menu.addItem(panel)
        let n = NSMenuItem(title: "Notifications", action: #selector(toggleNotify), keyEquivalent: "")
        n.state = settings.notify ? .on : .off
        n.target = self
        menu.addItem(n)
        let prefs = NSMenuItem(title: "Settings…", action: #selector(openSettings), keyEquivalent: ",")
        prefs.target = self
        menu.addItem(prefs)
        menu.addItem(NSMenuItem(title: "Quit", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q"))
    }

    func header(_ text: String, count: Int) -> NSMenuItem {
        let m = NSMenuItem()
        m.attributedTitle = NSAttributedString(string: "\(text.uppercased())  \(count)", attributes: [
            .font: NSFont.systemFont(ofSize: 10, weight: .semibold), .foregroundColor: Palette.gold, .kern: 0.6,
        ])
        m.isEnabled = false
        return m
    }

    func disabled(_ text: String) -> NSMenuItem {
        let m = NSMenuItem(title: text, action: nil, keyEquivalent: "")
        m.isEnabled = false
        return m
    }

    func row(_ s: Session) -> NSMenuItem {
        let mono = NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)
        let t = NSMutableAttributedString()
        t.append(NSAttributedString(string: s.state.glyph + "  ", attributes: [.foregroundColor: s.state.color, .font: mono]))
        t.append(NSAttributedString(string: s.name, attributes: [.font: NSFont.systemFont(ofSize: 13, weight: .medium)]))
        t.append(NSAttributedString(string: "  \(s.agent) · \(ago(s.since ?? s.lastActivity))", attributes: [.foregroundColor: NSColor.secondaryLabelColor, .font: NSFont.systemFont(ofSize: 11)]))
        let sub = subtitle(s)
        if !sub.isEmpty {
            t.append(NSAttributedString(string: "\n     " + String(sub.prefix(Tuning.rowSubtitleLimit)), attributes: [.foregroundColor: NSColor.tertiaryLabelColor, .font: NSFont.systemFont(ofSize: 11)]))
        }
        let m = NSMenuItem(title: s.name, action: #selector(openItem(_:)), keyEquivalent: "")
        m.attributedTitle = t
        m.representedObject = s.id
        m.target = self
        m.toolTip = [s.cwd, s.lastPrompt].compactMap { $0 }.joined(separator: "\n")
        return m
    }

    /// The row's second line: the title, led by the reason when the session needs you, else the last prompt.
    func subtitle(_ s: Session) -> String {
        let title = s.title ?? ""
        if s.state.needsYou, let detail = s.detail, !detail.isEmpty {
            return title.isEmpty ? detail : "\(detail) · \(title)"
        }
        return title.isEmpty ? s.lastPrompt ?? "" : title
    }

    /// A compact age: 42s, 7m, 3h05, 4d. "—" when the time is unknown.
    func ago(_ d: Date?) -> String {
        guard let d = d, d > .distantPast else { return "—" }
        let s = Int(Date().timeIntervalSince(d))
        if s < 60 { return "\(max(s, 0))s" }
        if s < 3600 { return "\(s / 60)m" }
        if s < 172800 { return "\(s / 3600)h\(String(format: "%02d", (s % 3600) / 60))" }
        return "\(s / 86400)d"
    }

    // MARK: actions

    @objc func openItem(_ sender: NSMenuItem) {
        if let id = sender.representedObject as? String { open(id) }
    }

    func open(_ id: String) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: cockpit)
        p.arguments = ["open", id]
        try? p.run()
    }

    @objc func openDashboard() { dashboard.show() }

    /// Shows the dashboard with a session selected.
    func showSession(_ id: String) {
        guard dashboard.isOpen else {
            dashboard.show(select: id)
            return
        }
        dashboard.show()
        let p = Process()
        p.executableURL = URL(fileURLWithPath: cockpit)
        p.arguments = ["select", id]
        try? p.run()
    }

    /// The hotkey: brings the dashboard to the front, or hides the app when the dashboard is already in
    /// front, so the same key goes back to what was there before.
    func toggleDashboard() {
        if NSApp.isActive && dashboard.isKey {
            NSApp.hide(nil)
        } else {
            dashboard.show()
        }
    }

    func registerHotKey() {
        let shortcut = settings.hotKey == "off" ? nil : Shortcut(settings.hotKey)
        settings.hotKeyTaken = !hotKey.register(shortcut)
    }

    @objc func openSettings() { settingsWindow.show() }

    /// Opening the app again (from Finder, Spotlight or the Dock) shows the dashboard window.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows: Bool) -> Bool {
        dashboard.show()
        return true
    }

    /// Opens the dashboard in a new Terminal window. The path travels as an argument to the script and
    /// `quoted form of` shell-quotes it, so spaces and quotes in it are neither split nor interpreted.
    @objc func openPanel() {
        let script = """
        on run argv
            tell application "Terminal"
                do script quoted form of (item 1 of argv)
                activate
            end tell
        end run
        """
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        p.arguments = ["-e", script, cockpit]
        try? p.run()
    }

    @objc func toggleNotify() { settings.notify.toggle() }

    // MARK: settings

    /// Applies a change made in the Settings window. Appearance applies at once; accounts, sources and the
    /// stuck time are read when the dashboard starts, so it restarts, once typing has paused.
    func settingsChanged(_ key: String) {
        dashboard.apply()
        registerHotKey()
        guard ["accounts", "sources", "stuckAfter"].contains(key), dashboard.isOpen else { return }
        restart?.cancel()
        let work = DispatchWorkItem { [weak self] in self?.dashboard.restart() }
        restart = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 2, execute: work)
    }

    /// The menu bar shown while the dashboard window is open: settings, the edit commands the terminal
    /// answers (copy, paste), and the window commands. There is no Quit: ⌘Q in the dashboard would also
    /// end the menu bar app, which lives on after the window closes.
    func mainMenu() -> NSMenu {
        let main = NSMenu()
        func submenu(_ title: String, _ items: [NSMenuItem]) {
            let m = NSMenu(title: title)
            items.forEach(m.addItem)
            let holder = NSMenuItem(title: title, action: nil, keyEquivalent: "")
            holder.submenu = m
            main.addItem(holder)
        }
        let prefs = NSMenuItem(title: "Settings…", action: #selector(openSettings), keyEquivalent: ",")
        prefs.target = self
        submenu("Agent Cockpit", [
            prefs,
            .separator(),
            NSMenuItem(title: "Hide Agent Cockpit", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h"),
        ])
        submenu("Edit", [
            NSMenuItem(title: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c"),
            NSMenuItem(title: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v"),
            NSMenuItem(title: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a"),
        ])
        submenu("Window", [
            NSMenuItem(title: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m"),
            NSMenuItem(title: "Close", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w"),
        ])
        return main
    }
}

let app = NSApplication.shared
let delegate = App()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
