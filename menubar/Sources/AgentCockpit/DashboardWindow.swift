import AppKit
import SwiftTerm

/// The dashboard in a window of its own: a terminal view running `cockpit`, so the dashboard is an app
/// with its own Dock icon and ⌘Tab entry instead of one more Terminal tab.
///
/// While the window is open the app is a regular app; when it closes the app goes back to living only
/// in the menu bar. Closing the window stops the dashboard but not the panes, which belong to
/// `cockpit serve`.
final class DashboardWindow: NSObject, NSWindowDelegate, LocalProcessTerminalViewDelegate {
    private let cockpit: String
    private var window: NSWindow?
    private var terminal: LocalProcessTerminalView?

    init(cockpit: String) {
        self.cockpit = cockpit
    }

    var isOpen: Bool { window != nil }

    /// Brings the window to the front, starting the dashboard if it is not running.
    func show() {
        if window == nil {
            open()
        }
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }

    private func open() {
        let frame = NSRect(x: 0, y: 0, width: 1440, height: 880)
        let w = NSWindow(contentRect: frame, styleMask: [.titled, .closable, .miniaturizable, .resizable],
                         backing: .buffered, defer: false)
        w.title = "Agent Cockpit"
        w.isReleasedWhenClosed = false
        w.backgroundColor = Theme.background
        w.titlebarAppearsTransparent = true
        w.appearance = NSAppearance(named: .darkAqua)
        w.delegate = self
        w.center()
        w.setFrameAutosaveName("Dashboard")

        let t = LocalProcessTerminalView(frame: frame)
        t.processDelegate = self
        t.font = NSFont.monospacedSystemFont(ofSize: 13, weight: .regular)
        t.nativeBackgroundColor = Theme.background
        t.nativeForegroundColor = Theme.ink
        t.caretColor = Theme.gold
        t.optionAsMetaKey = false // option types accented characters, as in any Mac text field
        w.contentView = t

        window = w
        terminal = t
        // A login shell gives cockpit the PATH the person's shell has (agents, external sources),
        // which an app started by launchd does not.
        let shell = ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
        t.startProcess(executable: shell, args: ["-l", "-c", "exec \(shellQuoted(cockpit))"],
                       environment: environment(), execName: "-" + (shell as NSString).lastPathComponent)
        w.makeFirstResponder(t)
    }

    /// The dashboard's environment: the app's own, with what a terminal would announce. TERM_PROGRAM
    /// tells cockpit it may use 24-bit color.
    private func environment() -> [String] {
        var env = ProcessInfo.processInfo.environment
        env["TERM"] = "xterm-256color"
        env["COLORTERM"] = "truecolor"
        env["TERM_PROGRAM"] = "agent-cockpit"
        env["LANG"] = env["LANG"] ?? "en_US.UTF-8"
        return env.map { "\($0.key)=\($0.value)" }
    }

    // MARK: NSWindowDelegate

    func windowWillClose(_ notification: Notification) {
        terminal?.terminate()
        terminal = nil
        window = nil
        NSApp.setActivationPolicy(.accessory)
    }

    // MARK: LocalProcessTerminalViewDelegate

    func sizeChanged(source: LocalProcessTerminalView, newCols: Int, newRows: Int) {}

    func setTerminalTitle(source: LocalProcessTerminalView, title: String) {}

    func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}

    /// The dashboard quit (q): the window has nothing left to show.
    func processTerminated(source: TerminalView, exitCode: Int32?) {
        DispatchQueue.main.async { [weak self] in self?.window?.close() }
    }
}

/// The dashboard's own colors (internal/tui/theme.go), so the window around the terminal matches it.
enum Theme {
    static let background = NSColor(srgbRed: 0x14 / 255, green: 0x11 / 255, blue: 0x0c / 255, alpha: 1)
    static let ink = NSColor(srgbRed: 0xe9 / 255, green: 0xdf / 255, blue: 0xc9 / 255, alpha: 1)
    static let gold = NSColor(srgbRed: 0xc0 / 255, green: 0x8a / 255, blue: 0x2e / 255, alpha: 1)
}

/// s quoted for a POSIX shell.
func shellQuoted(_ s: String) -> String {
    "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
}
