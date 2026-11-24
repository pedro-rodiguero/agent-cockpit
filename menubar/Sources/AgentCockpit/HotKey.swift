import Carbon.HIToolbox

/// A keyboard shortcut that works from any app. It uses Carbon's hot keys, which need no Accessibility
/// permission, unlike watching every key press.
final class HotKey {
    var action: (() -> Void)?
    private var ref: EventHotKeyRef?
    private var handler: EventHandlerRef?

    init() {
        var spec = EventTypeSpec(eventClass: OSType(kEventClassKeyboard), eventKind: UInt32(kEventHotKeyPressed))
        InstallEventHandler(GetApplicationEventTarget(), { _, _, data in
            guard let data else { return OSStatus(eventNotHandledErr) }
            let hotKey = Unmanaged<HotKey>.fromOpaque(data).takeUnretainedValue()
            DispatchQueue.main.async { hotKey.action?() }
            return noErr
        }, 1, &spec, Unmanaged.passUnretained(self).toOpaque(), &handler)
    }

    /// Registers shortcut in place of the previous one; nil registers none. Returns false when another
    /// app already holds the shortcut.
    @discardableResult
    func register(_ shortcut: Shortcut?) -> Bool {
        if let ref {
            UnregisterEventHotKey(ref)
            self.ref = nil
        }
        guard let shortcut else { return true }
        let id = EventHotKeyID(signature: OSType(0x434B_5054), id: 1) // "CKPT"
        return RegisterEventHotKey(shortcut.keyCode, shortcut.modifiers, id, GetApplicationEventTarget(), 0, &ref) == noErr
    }
}

/// A shortcut as the settings file writes it: modifiers and a key joined by "+", like "option+space"
/// or "control+shift+k".
struct Shortcut: Equatable {
    let keyCode: UInt32
    let modifiers: UInt32
    /// How menus write it: ⌥Space.
    let label: String

    init?(_ text: String) {
        var mods: UInt32 = 0
        var symbols = ""
        var key: (code: Int, name: String)?
        for part in text.lowercased().split(separator: "+").map({ $0.trimmingCharacters(in: .whitespaces) }) {
            switch part {
            case "control", "ctrl": mods |= UInt32(controlKey)
            case "option", "alt": mods |= UInt32(optionKey)
            case "shift": mods |= UInt32(shiftKey)
            case "command", "cmd": mods |= UInt32(cmdKey)
            default:
                guard key == nil, let k = Shortcut.keys[part] else { return nil }
                key = (k, part == "space" ? "Space" : part == "grave" ? "`" : part.uppercased())
            }
        }
        guard let key, mods != 0 else { return nil }
        // Menus list modifiers in this order.
        if mods & UInt32(controlKey) != 0 { symbols += "⌃" }
        if mods & UInt32(optionKey) != 0 { symbols += "⌥" }
        if mods & UInt32(shiftKey) != 0 { symbols += "⇧" }
        if mods & UInt32(cmdKey) != 0 { symbols += "⌘" }
        keyCode = UInt32(key.code)
        modifiers = mods
        label = symbols + key.name
    }

    private static let keys: [String: Int] = {
        var k: [String: Int] = ["space": kVK_Space, "grave": kVK_ANSI_Grave]
        let letters: [Int] = [kVK_ANSI_A, kVK_ANSI_B, kVK_ANSI_C, kVK_ANSI_D, kVK_ANSI_E, kVK_ANSI_F, kVK_ANSI_G,
                              kVK_ANSI_H, kVK_ANSI_I, kVK_ANSI_J, kVK_ANSI_K, kVK_ANSI_L, kVK_ANSI_M, kVK_ANSI_N,
                              kVK_ANSI_O, kVK_ANSI_P, kVK_ANSI_Q, kVK_ANSI_R, kVK_ANSI_S, kVK_ANSI_T, kVK_ANSI_U,
                              kVK_ANSI_V, kVK_ANSI_W, kVK_ANSI_X, kVK_ANSI_Y, kVK_ANSI_Z]
        for (i, code) in letters.enumerated() {
            k[String(UnicodeScalar(UInt8(97 + i)))] = code
        }
        let digits: [Int] = [kVK_ANSI_0, kVK_ANSI_1, kVK_ANSI_2, kVK_ANSI_3, kVK_ANSI_4, kVK_ANSI_5, kVK_ANSI_6,
                             kVK_ANSI_7, kVK_ANSI_8, kVK_ANSI_9]
        for (i, code) in digits.enumerated() {
            k[String(i)] = code
        }
        return k
    }()
}
