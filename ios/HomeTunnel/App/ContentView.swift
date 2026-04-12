// Copyright (c) 2026 d991d. All rights reserved.

import SwiftUI
import NetworkExtension

struct ContentView: View {
    @EnvironmentObject var vpn: VPNManager

    var body: some View {
        Group {
            switch vpn.status {
            case .connected:
                ConnectedView()
            case .connecting, .reasserting:
                ConnectingView()
            default:
                ConnectView()
            }
        }
        .animation(.easeInOut(duration: 0.3), value: vpn.status.rawValue)
    }
}
