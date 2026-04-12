// Copyright (c) 2026 d991d. All rights reserved.

import SwiftUI

struct ConnectingView: View {
    @EnvironmentObject var vpn: VPNManager
    @State private var pulse = false

    var body: some View {
        VStack(spacing: 36) {

            Spacer()

            ZStack {
                // Pulsing ring
                Circle()
                    .stroke(.tint.opacity(0.2), lineWidth: 2)
                    .frame(width: pulse ? 140 : 100, height: pulse ? 140 : 100)
                    .animation(.easeInOut(duration: 1.2).repeatForever(autoreverses: true), value: pulse)

                Circle()
                    .fill(.tint.opacity(0.1))
                    .frame(width: 100, height: 100)

                Image(systemName: "shield.lefthalf.filled")
                    .font(.system(size: 48))
                    .foregroundStyle(.tint)
            }
            .onAppear { pulse = true }

            VStack(spacing: 8) {
                Text("Connecting…")
                    .font(.title2.bold())

                Text("Completing handshake with your home server")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }

            Spacer()

            Button(role: .destructive) {
                vpn.disconnect()
            } label: {
                Label("Cancel", systemImage: "xmark.circle")
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 14)
            }
            .buttonStyle(.bordered)
            .controlSize(.large)
            .padding(.horizontal)
            .padding(.bottom, 40)
        }
        .padding()
    }
}
