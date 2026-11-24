// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "AgentCockpit",
    platforms: [.macOS(.v13)],
    dependencies: [
        // The terminal emulator the dashboard window runs cockpit in. Pinned: a new release is a
        // deliberate update, checked in the window before it lands.
        .package(url: "https://github.com/migueldeicaza/SwiftTerm", exact: "1.20.0"),
    ],
    targets: [
        .executableTarget(
            name: "AgentCockpit",
            dependencies: [.product(name: "SwiftTerm", package: "SwiftTerm")],
            path: "Sources/AgentCockpit"
        ),
    ]
)
