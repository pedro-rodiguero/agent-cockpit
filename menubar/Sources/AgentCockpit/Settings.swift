import AppKit
import Combine

/// cockpit's settings file, <Application Support>/agent-cockpit/config.json (internal/config). The app
/// reads and writes it as a plain JSON object, so keys it does not know about survive its writes;
/// cockpit only reads it, and picks most changes up on its next collection pass.
final class CockpitSettings: ObservableObject {
    /// Posted after the app writes the file. `userInfo["key"]` is the top-level key that changed.
    static let changed = Notification.Name("CockpitSettingsChanged")

    let url: URL = {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
        return support.appendingPathComponent("agent-cockpit/config.json")
    }()

    @Published private(set) var raw: [String: Any] = [:]
    /// Set while the file on disk does not parse. Writes are refused then, so a hand edit in progress is
    /// not overwritten.
    @Published private(set) var problem: String?
    /// Set by the app when another app already holds the hotkey.
    @Published var hotKeyTaken = false
    private var modified: Date?

    init() { reload() }

    /// Reads the file again when it changed on disk, and reports whether it did.
    @discardableResult
    func reload() -> Bool {
        let date = (try? FileManager.default.attributesOfItem(atPath: url.path))?[.modificationDate] as? Date
        guard date != modified else { return false }
        modified = date
        guard let data = try? Data(contentsOf: url) else {
            raw = [:]
            problem = nil
            return true
        }
        if let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
            raw = object
            problem = nil
        } else {
            problem = "\((url.path as NSString).abbreviatingWithTildeInPath) is not valid JSON"
        }
        return true
    }

    func value(_ path: [String]) -> Any? {
        var node: Any? = raw
        for key in path { node = (node as? [String: Any])?[key] }
        return node
    }

    /// Sets the value at path and writes the file. nil, "" and empty lists or objects remove the key, so
    /// the file only holds what differs from the defaults.
    func set(_ value: Any?, _ path: [String]) {
        guard problem == nil, let first = path.first else { return }
        raw = setting(raw, Array(path), to: isEmpty(value) ? nil : value)
        write()
        NotificationCenter.default.post(name: Self.changed, object: self, userInfo: ["key": first])
    }

    private func setting(_ object: [String: Any], _ path: [String], to value: Any?) -> [String: Any] {
        var object = object
        let key = path[0]
        if path.count == 1 {
            object[key] = value
        } else {
            let child = setting(object[key] as? [String: Any] ?? [:], Array(path.dropFirst()), to: value)
            object[key] = child.isEmpty ? nil : child
        }
        return object
    }

    private func isEmpty(_ value: Any?) -> Bool {
        switch value {
        case nil: return true
        case let s as String: return s.isEmpty
        case let a as [Any]: return a.isEmpty
        case let d as [String: Any]: return d.isEmpty
        default: return false
        }
    }

    private func write() {
        do {
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            let data = try JSONSerialization.data(withJSONObject: raw, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes])
            try data.write(to: url, options: .atomic)
            modified = (try? FileManager.default.attributesOfItem(atPath: url.path))?[.modificationDate] as? Date
        } catch {
            problem = "could not save settings: \(error.localizedDescription)"
        }
    }

    // MARK: typed fields (docs/configuration.md)

    var theme: String {
        get { value(["theme"]) as? String ?? "dark" }
        set { set(newValue == "dark" ? nil : newValue, ["theme"]) }
    }
    var terminalBackground: Bool {
        get { value(["background"]) as? String == "terminal" }
        set { set(newValue ? "terminal" : nil, ["background"]) }
    }
    var quietAfter: String {
        get { value(["quietAfter"]) as? String ?? "1h" }
        set { set(newValue == "1h" ? nil : newValue, ["quietAfter"]) }
    }
    var stuckAfter: String {
        get { value(["stuckAfter"]) as? String ?? "10m" }
        set { set(newValue == "10m" ? nil : newValue, ["stuckAfter"]) }
    }
    var claude: [String] {
        get { value(["accounts", "claude"]) as? [String] ?? [] }
        set { set(newValue, ["accounts", "claude"]) }
    }
    var codex: String {
        get { value(["accounts", "codex"]) as? String ?? "" }
        set { set(newValue, ["accounts", "codex"]) }
    }
    var agy: String {
        get { value(["accounts", "agy"]) as? String ?? "" }
        set { set(newValue, ["accounts", "agy"]) }
    }
    var sourcesOff: [String] {
        get { value(["sources", "off"]) as? [String] ?? [] }
        set { set(newValue, ["sources", "off"]) }
    }
    var defaultGroup: String {
        get { value(["todo", "defaultGroup"]) as? String ?? "" }
        set { set(newValue, ["todo", "defaultGroup"]) }
    }
    var fold: [String: String] {
        get { value(["todo", "fold"]) as? [String: String] ?? [:] }
        set { set(newValue, ["todo", "fold"]) }
    }
    var foldWhenReachable: String {
        get { value(["todo", "foldWhenReachable"]) as? String ?? "" }
        set { set(newValue, ["todo", "foldWhenReachable"]) }
    }
    var opacity: Double {
        get { (value(["app", "opacity"]) as? NSNumber)?.doubleValue ?? 1 }
        set { set(newValue >= 1 ? nil : (newValue * 100).rounded() / 100, ["app", "opacity"]) }
    }
    var blur: Bool {
        get { (value(["app", "blur"]) as? NSNumber)?.boolValue ?? true }
        set { set(newValue ? nil : false, ["app", "blur"]) }
    }
    var font: String {
        get { value(["app", "font"]) as? String ?? "" }
        set { set(newValue, ["app", "font"]) }
    }
    var fontSize: Double {
        get { (value(["app", "fontSize"]) as? NSNumber)?.doubleValue ?? 13 }
        set { set(newValue == 13 ? nil : newValue, ["app", "fontSize"]) }
    }
    /// The shortcut that shows and hides the dashboard from any app, or "off".
    var hotKey: String {
        get { value(["app", "hotKey"]) as? String ?? "option+space" }
        set { set(newValue == "option+space" ? nil : newValue, ["app", "hotKey"]) }
    }
    var notify: Bool {
        get { (value(["app", "notify"]) as? NSNumber)?.boolValue ?? true }
        set { set(newValue ? nil : false, ["app", "notify"]) }
    }

    // MARK: Claude profiles

    /// The profiles cockpit watches: the configured list, or the detected one when none is configured.
    func claudeOn(_ detected: [String]) -> [String] { claude.isEmpty ? detected : claude }

    /// Turns one profile on or off. A list equal to the detected one is not written down, so profiles
    /// added later are picked up again.
    func setClaude(_ dir: String, on: Bool, detected: [String]) {
        var list = claudeOn(detected).filter { $0 != dir }
        if on { list.append(dir) }
        claude = list == detected ? [] : list
    }
}

/// What cockpit found on the machine (`cockpit config`): the profiles and sources it can collect from.
final class Detected: ObservableObject {
    struct Found: Decodable {
        var claude: [String] = []
        var codex = ""
        var agy = ""
        var sources: [String] = []
    }

    private struct Output: Decodable { let detected: Found }

    @Published private(set) var found = Found()
    private let cockpit: String

    init(cockpit: String) {
        self.cockpit = cockpit
        refresh()
    }

    func refresh() {
        let binary = cockpit
        DispatchQueue.global(qos: .userInitiated).async {
            guard let run = try? ProcessOutput.run(binary, arguments: ["config"]), run.status == 0,
                  let out = try? JSONDecoder().decode(Output.self, from: run.stdout) else { return }
            DispatchQueue.main.async { self.found = out.detected }
        }
    }
}

/// Opening the app at login: the LaunchAgent scripts/install.sh writes. Turning it on only writes the
/// file, which launchd reads at the next login; loading it now would open the app a second time.
enum LoginItem {
    static let label = "io.github.pedro-rodiguero.agent-cockpit"

    static var plist: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/LaunchAgents/\(label).plist")
    }

    static var isOn: Bool { FileManager.default.fileExists(atPath: plist.path) }

    static func set(_ on: Bool) {
        if on {
            let agent: [String: Any] = [
                "Label": label,
                "ProgramArguments": ["/usr/bin/open", "-a", Bundle.main.bundlePath, "--args", "--background"],
                "RunAtLoad": true,
            ]
            try? FileManager.default.createDirectory(at: plist.deletingLastPathComponent(), withIntermediateDirectories: true)
            if let data = try? PropertyListSerialization.data(fromPropertyList: agent, format: .xml, options: 0) {
                try? data.write(to: plist, options: .atomic)
            }
        } else {
            let p = Process()
            p.executableURL = URL(fileURLWithPath: "/bin/launchctl")
            p.arguments = ["bootout", "gui/\(getuid())/\(label)"]
            p.standardError = FileHandle.nullDevice
            try? p.run()
            p.waitUntilExit()
            try? FileManager.default.removeItem(at: plist)
        }
    }
}
