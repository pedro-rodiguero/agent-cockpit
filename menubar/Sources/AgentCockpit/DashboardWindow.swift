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
    private let settings: CockpitSettings
    private var window: NSWindow?
    private var terminal: LocalProcessTerminalView?
    private var blur: NSVisualEffectView?
    private var appearanceObservation: NSKeyValueObservation?

    init(cockpit: String, settings: CockpitSettings) {
        self.cockpit = cockpit
        self.settings = settings
    }

    var isOpen: Bool { window != nil }

    /// Whether the window is the one taking keys.
    var isKey: Bool { window?.isKeyWindow ?? false }

    /// Brings the window to the front, starting the dashboard if it is not running. A session to select
    /// goes to a dashboard started now; a running one is told with `cockpit select`.
    func show(select session: String? = nil) {
        if window == nil {
            open(select: session)
        }
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }

    /// Starts the dashboard again, for settings it reads only at startup (accounts, sources). Panes are
    /// unaffected: they live in the pane server.
    func restart() {
        guard let w = window, let old = terminal else { return }
        old.processDelegate = nil
        old.terminate()
        let t = makeTerminal(frame: old.frame)
        old.removeFromSuperview()
        w.contentView?.addSubview(t)
        terminal = t
        apply()
        start(t)
        w.makeFirstResponder(t)
    }

    private func open(select session: String?) {
        let frame = NSRect(x: 0, y: 0, width: 1440, height: 880)
        let w = NSWindow(contentRect: frame, styleMask: [.titled, .closable, .miniaturizable, .resizable],
                         backing: .buffered, defer: false)
        w.title = "Agent Cockpit"
        w.isReleasedWhenClosed = false
        w.titlebarAppearsTransparent = true
        w.delegate = self
        w.center()
        w.setFrameAutosaveName("Dashboard")

        // Behind the terminal, a blur of the desktop, seen where the terminal's background is translucent.
        let container = NSView(frame: frame)
        let effect = NSVisualEffectView(frame: frame)
        effect.autoresizingMask = [.width, .height]
        effect.blendingMode = .behindWindow
        effect.material = .underWindowBackground
        effect.state = .active
        container.addSubview(effect)
        let t = makeTerminal(frame: frame)
        container.addSubview(t)
        w.contentView = container

        window = w
        terminal = t
        blur = effect
        apply()
        appearanceObservation = NSApp.observe(\.effectiveAppearance) { [weak self] _, _ in
            DispatchQueue.main.async { self?.apply() }
        }
        start(t, select: session)
        w.makeFirstResponder(t)
    }

    private func makeTerminal(frame: NSRect) -> LocalProcessTerminalView {
        let t = LocalProcessTerminalView(frame: frame)
        t.autoresizingMask = [.width, .height]
        t.processDelegate = self
        t.optionAsMetaKey = false // option types accented characters, as in any Mac text field
        return t
    }

    private func start(_ t: LocalProcessTerminalView, select session: String? = nil) {
        // A login shell gives cockpit the PATH the person's shell has (agents, external sources),
        // which an app started by launchd does not.
        let shell = ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
        var command = "exec \(shellQuoted(cockpit))"
        if let session { command += " -select \(shellQuoted(session))" }
        t.startProcess(executable: shell, args: ["-l", "-c", command],
                       environment: environment(), execName: "-" + (shell as NSString).lastPathComponent)
    }

    /// Applies the appearance settings: theme colors, background opacity and blur, font. cockpit itself
    /// picks the theme up on its next collection pass.
    func apply() {
        guard let w = window, let t = terminal else { return }
        let theme = Theme.current(settings.theme)
        let opacity = settings.opacity
        w.appearance = NSAppearance(named: theme.dark ? .darkAqua : .aqua)
        w.isOpaque = opacity >= 1
        w.backgroundColor = opacity >= 1 ? theme.background : .clear
        blur?.isHidden = opacity >= 1 || !settings.blur
        t.nativeForegroundColor = theme.ink
        t.nativeBackgroundColor = theme.background
        t.backgroundOpacity = opacity
        t.caretColor = theme.gold
        let size = CGFloat(settings.fontSize)
        let font = settings.font.isEmpty ? nil : NSFont(name: settings.font, size: size)
        let wanted = font ?? NSFont.monospacedSystemFont(ofSize: size, weight: .regular)
        if t.font != wanted { t.font = wanted }
    }

    /// The dashboard's environment: the app's own, with what a terminal would announce. TERM_PROGRAM
    /// tells cockpit it may use 24-bit color, and that a translucent window is this one.
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
        appearanceObservation = nil
        terminal?.processDelegate = nil
        terminal?.terminate()
        terminal = nil
        blur = nil
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
struct Theme {
    let dark: Bool
    let background, ink, gold: NSColor

    static let darkTheme = Theme(dark: true, background: rgb(0x14, 0x11, 0x0c), ink: rgb(0xe9, 0xdf, 0xc9), gold: rgb(0xc0, 0x8a, 0x2e))
    static let lightTheme = Theme(dark: false, background: rgb(0xf6, 0xf1, 0xe6), ink: rgb(0x2a, 0x23, 0x18), gold: rgb(0x96, 0x64, 0x10))

    /// The theme a setting names; "system" follows the system's appearance.
    static func current(_ setting: String) -> Theme {
        switch setting {
        case "light": return lightTheme
        case "system":
            return NSApp.effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? darkTheme : lightTheme
        default: return darkTheme
        }
    }

    private static func rgb(_ r: Int, _ g: Int, _ b: Int) -> NSColor {
        NSColor(srgbRed: CGFloat(r) / 255, green: CGFloat(g) / 255, blue: CGFloat(b) / 255, alpha: 1)
    }
}

/// s quoted for a POSIX shell.
func shellQuoted(_ s: String) -> String {
    "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'"
}
