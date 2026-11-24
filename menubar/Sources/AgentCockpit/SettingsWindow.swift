import AppKit
import SwiftUI

/// The Settings window (⌘,): a form over the settings file.
final class SettingsWindow {
    private let settings: CockpitSettings
    private let detected: Detected
    private var window: NSWindow?

    init(settings: CockpitSettings, cockpit: String) {
        self.settings = settings
        detected = Detected(cockpit: cockpit)
    }

    func show() {
        settings.reload()
        detected.refresh()
        if window == nil {
            let host = NSHostingController(rootView: SettingsView(settings: settings, detected: detected))
            let w = NSWindow(contentViewController: host)
            w.title = "Agent Cockpit Settings"
            w.styleMask = [.titled, .closable]
            w.isReleasedWhenClosed = false
            w.center()
            window = w
        }
        NSApp.activate(ignoringOtherApps: true)
        window?.makeKeyAndOrderFront(nil)
    }
}

struct SettingsView: View {
    @ObservedObject var settings: CockpitSettings
    @ObservedObject var detected: Detected

    var body: some View {
        VStack(spacing: 0) {
            if let problem = settings.problem {
                Text("\(problem) · fix it or delete it to change settings here")
                    .font(.callout)
                    .foregroundColor(.red)
                    .padding(.top, 10)
            }
            TabView {
                AccountsPane(settings: settings, detected: detected)
                    .tabItem { Label("Accounts", systemImage: "person.2") }
                AppearancePane(settings: settings)
                    .tabItem { Label("Appearance", systemImage: "paintpalette") }
                TodoPane(settings: settings)
                    .tabItem { Label("To Do", systemImage: "checklist") }
                GeneralPane(settings: settings)
                    .tabItem { Label("General", systemImage: "gearshape") }
            }
            .disabled(settings.problem != nil)
        }
        .frame(width: 540, height: 470)
    }
}

// MARK: - Accounts

struct AccountsPane: View {
    @ObservedObject var settings: CockpitSettings
    @ObservedObject var detected: Detected

    var body: some View {
        let found = detected.found
        let on = settings.claudeOn(found.claude)
        let all = found.claude + settings.claude.filter { !found.claude.contains($0) }
        Form {
            Section {
                ForEach(all, id: \.self) { dir in
                    Toggle(isOn: Binding(get: { on.contains(dir) },
                                         set: { settings.setClaude(dir, on: $0, detected: found.claude) })) {
                        Text(tilde(dir)).font(.system(.body, design: .monospaced))
                    }
                    .disabled(on == [dir]) // the last one: turn the claude source off instead
                }
                Button("Add Profile Folder…") {
                    if let dir = chooseFolder() {
                        var list = on
                        if !list.contains(dir) { list.append(dir) }
                        settings.claude = list
                    }
                }
            } header: {
                Text("Claude Code profiles")
            } footer: {
                Text("Each folder is a CLAUDE_CONFIG_DIR. ~/.claude-<name> folders are found on their own.")
                    .foregroundColor(.secondary)
            }
            Section("Other agents") {
                FolderField(label: "Codex", placeholder: found.codex, path: $settings.codex)
                FolderField(label: "agy", placeholder: found.agy, path: $settings.agy)
            }
            Section {
                ForEach(found.sources, id: \.self) { name in
                    Toggle(name, isOn: Binding(get: { !settings.sourcesOff.contains(name) },
                                               set: { on in
                                                   settings.sourcesOff = on ? settings.sourcesOff.filter { $0 != name }
                                                       : settings.sourcesOff + [name]
                                               }))
                }
            } header: {
                Text("Sources")
            } footer: {
                Text("External sources are the cockpit-source-* programs on your PATH. Changes on this tab restart the dashboard; panes keep running.")
                    .foregroundColor(.secondary)
            }
        }
        .formStyle(.grouped)
    }
}

struct FolderField: View {
    let label: String
    let placeholder: String
    @Binding var path: String

    var body: some View {
        HStack {
            TextField(label, text: $path, prompt: Text(tilde(placeholder)))
            Button("Choose…") {
                if let dir = chooseFolder() { path = dir }
            }
        }
    }
}

// MARK: - Appearance

struct AppearancePane: View {
    @ObservedObject var settings: CockpitSettings
    private let fonts: [String] = NSFontManager.shared.availableFontFamilies.filter {
        NSFont(name: $0, size: 12)?.isFixedPitch ?? false
    }

    var body: some View {
        Form {
            Section {
                Picker("Theme", selection: $settings.theme) {
                    Text("System").tag("system")
                    Text("Dark").tag("dark")
                    Text("Light").tag("light")
                }
                .pickerStyle(.segmented)
                Toggle("Let the terminal's own background show through", isOn: $settings.terminalBackground)
            } footer: {
                Text("For a Terminal.app or iTerm profile with a background image or transparency. The app's window below has its own setting.")
                    .foregroundColor(.secondary)
            }
            Section("Dashboard window") {
                HStack {
                    Slider(value: $settings.opacity, in: 0.5...1, step: 0.05) { Text("Opacity") }
                    Text("\(Int((settings.opacity * 100).rounded()))%")
                        .monospacedDigit()
                        .frame(width: 44, alignment: .trailing)
                }
                Toggle("Blur what is behind the window", isOn: $settings.blur)
                    .disabled(settings.opacity >= 1)
                Picker("Font", selection: $settings.font) {
                    Text("System monospaced").tag("")
                    ForEach(fonts, id: \.self) { Text($0).tag($0) }
                }
                Stepper(value: $settings.fontSize, in: 9...24, step: 1) {
                    Text("Font size: \(Int(settings.fontSize))")
                }
            }
        }
        .formStyle(.grouped)
    }
}

// MARK: - To Do

struct TodoPane: View {
    @ObservedObject var settings: CockpitSettings
    @State private var from = ""
    @State private var into = ""

    var body: some View {
        Form {
            Section {
                TextField("Default group", text: $settings.defaultGroup, prompt: Text("Tasks"))
            } footer: {
                Text("The group of a task added without #group.").foregroundColor(.secondary)
            }
            Section {
                ForEach(settings.fold.keys.sorted(), id: \.self) { group in
                    HStack {
                        Text(group)
                        Image(systemName: "arrow.right").foregroundColor(.secondary)
                        Text(settings.fold[group] ?? "")
                        Spacer()
                        Button {
                            var f = settings.fold
                            f[group] = nil
                            settings.fold = f
                        } label: {
                            Image(systemName: "minus.circle")
                        }
                        .buttonStyle(.borderless)
                    }
                }
                HStack {
                    TextField("Group", text: $from)
                    Image(systemName: "arrow.right").foregroundColor(.secondary)
                    TextField("Shown inside", text: $into)
                    Button("Add") {
                        var f = settings.fold
                        f[from.trimmingCharacters(in: .whitespaces)] = into.trimmingCharacters(in: .whitespaces)
                        settings.fold = f
                        from = ""
                        into = ""
                    }
                    .disabled(from.trimmingCharacters(in: .whitespaces).isEmpty || into.trimmingCharacters(in: .whitespaces).isEmpty)
                }
                TextField("Only while reachable", text: $settings.foldWhenReachable, prompt: Text("host:port (optional)"))
            } header: {
                Text("Fold groups")
            } footer: {
                Text("Shows one group's tasks inside another, marked with their own group. With an address, only while it answers: a way to say \"while I'm on the office network\".")
                    .foregroundColor(.secondary)
            }
        }
        .formStyle(.grouped)
    }
}

// MARK: - General

struct GeneralPane: View {
    @ObservedObject var settings: CockpitSettings
    @State private var atLogin = LoginItem.isOn

    var body: some View {
        Form {
            Section {
                Toggle("Notify when a session needs me", isOn: $settings.notify)
                Toggle("Open at login (in the menu bar)", isOn: $atLogin)
                    .onChange(of: atLogin) { LoginItem.set($0) }
            }
            Section {
                DurationPicker(label: "Idle sessions move to the side after", value: $settings.quietAfter,
                               options: ["15m", "30m", "1h", "2h", "4h", "8h"])
                DurationPicker(label: "A silent working session is stuck after", value: $settings.stuckAfter,
                               options: ["5m", "10m", "15m", "30m", "1h"])
            }
            Section {
                HStack {
                    Text(tilde(settings.url.path))
                        .font(.system(.callout, design: .monospaced))
                        .foregroundColor(.secondary)
                        .textSelection(.enabled)
                    Spacer()
                    Button("Show in Finder") {
                        let fm = FileManager.default
                        if fm.fileExists(atPath: settings.url.path) {
                            NSWorkspace.shared.activateFileViewerSelecting([settings.url])
                        } else {
                            NSWorkspace.shared.open(settings.url.deletingLastPathComponent())
                        }
                    }
                }
            } header: {
                Text("Settings file")
            } footer: {
                Text("Everything here is in this file; docs/configuration.md describes it.").foregroundColor(.secondary)
            }
        }
        .formStyle(.grouped)
    }
}

struct DurationPicker: View {
    let label: String
    @Binding var value: String
    let options: [String]

    var body: some View {
        Picker(label, selection: $value) {
            // A value written by hand that is not one of the options still shows.
            ForEach(options.contains(value) ? options : [value] + options, id: \.self) { Text($0).tag($0) }
        }
    }
}

// MARK: - Helpers

func tilde(_ path: String) -> String { (path as NSString).abbreviatingWithTildeInPath }

func chooseFolder() -> String? {
    let panel = NSOpenPanel()
    panel.canChooseDirectories = true
    panel.canChooseFiles = false
    panel.showsHiddenFiles = true
    panel.directoryURL = FileManager.default.homeDirectoryForCurrentUser
    return panel.runModal() == .OK ? panel.url?.path : nil
}
