import AppKit
import SwiftTerm

/// A terminal that takes files dropped on it, as Terminal does: their paths are pasted, escaped for a
/// shell, so an agent in the pane being typed into gets them (Claude Code attaches a dropped image).
final class DropTerminalView: LocalProcessTerminalView {
    override init(frame: CGRect) {
        super.init(frame: frame)
        registerForDraggedTypes([.fileURL])
    }

    required init?(coder: NSCoder) {
        super.init(coder: coder)
        registerForDraggedTypes([.fileURL])
    }

    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        fileURLs(sender).isEmpty ? [] : .copy
    }

    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        let urls = fileURLs(sender)
        guard !urls.isEmpty else { return false }
        var text = urls.map { shellEscaped($0.path) }.joined(separator: " ") + " "
        if getTerminal().bracketedPasteMode {
            text = "\u{1b}[200~" + text + "\u{1b}[201~"
        }
        send(data: ArraySlice(Array(text.utf8)))
        window?.makeKeyAndOrderFront(nil)
        window?.makeFirstResponder(self)
        return true
    }

    private func fileURLs(_ sender: NSDraggingInfo) -> [URL] {
        sender.draggingPasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL] ?? []
    }
}

/// shellEscaped backslash-escapes the characters a shell would split or expand, the way Terminal writes
/// a dropped path. Control characters are dropped first: a file name may hold ESC and newlines, and with
/// them could end a bracketed paste early or submit a command line of its own.
private func shellEscaped(_ path: String) -> String {
    let special = Set(" \t\\'\"()[]{}&;|<>*?$`!#~=%")
    var printable = String.UnicodeScalarView()
    printable.append(contentsOf: path.unicodeScalars.filter { $0.value >= 0x20 && !(0x7f...0x9f).contains($0.value) })
    return String(String(printable).flatMap { special.contains($0) ? ["\\", $0] : [$0] })
}
