// Copyright (c) 2026 d991d. All rights reserved.

import SwiftUI

@main
struct HomeTunnelApp: App {
    @StateObject private var vpnManager = VPNManager.shared

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environmentObject(vpnManager)
        }
    }
}
