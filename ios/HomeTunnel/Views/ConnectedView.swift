// Copyright (c) 2026 d991d. All rights reserved.

import SwiftUI

struct ConnectedView: View {
    @EnvironmentObject var vpn: VPNManager
    @State private var showDisconnectConfirm = false

    var body: some View {
        NavigationStack {
            VStack(spacing: 28) {

                Spacer()

                // ── Status badge ──────────────────────────────────────────────
                VStack(spacing: 16) {
                    ZStack {
                        Circle()
                            .fill(.green.opacity(0.12))
                            .frame(width: 110, height: 110)
                        Image(systemName: "shield.fill")
                            .font(.system(size: 56))
                            .foregroundStyle(.green)
                    }

                    VStack(spacing: 4) {
                        Text("Protected")
                            .font(.title.bold())
                            .foregroundStyle(.green)

                        if !vpn.virtualIP.isEmpty {
                            Text("Virtual IP: \(vpn.virtualIP)")
                                .font(.subheadline.monospaced())
                                .foregroundStyle(.secondary)
                        }
                    }
                }

                // ── Traffic stats ─────────────────────────────────────────────
                HStack(spacing: 0) {
                    StatCell(
                        icon: "arrow.down.circle.fill",
                        iconColor: .blue,
                        label: "Downloaded",
                        value: formatBytes(vpn.bytesIn)
                    )

                    Divider().frame(height: 44)

                    StatCell(
                        icon: "arrow.up.circle.fill",
                        iconColor: .orange,
                        label: "Uploaded",
                        value: formatBytes(vpn.bytesOut)
                    )
                }
                .padding(.vertical, 16)
                .background(.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 16))
                .padding(.horizontal)

                Spacer()

                // ── Disconnect ────────────────────────────────────────────────
                Button(role: .destructive) {
                    showDisconnectConfirm = true
                } label: {
                    Label("Disconnect", systemImage: "stop.circle.fill")
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 14)
                        .font(.headline)
                }
                .buttonStyle(.bordered)
                .tint(.red)
                .controlSize(.large)
                .padding(.horizontal)
                .padding(.bottom, 40)
            }
            .navigationTitle("HomeTunnel")
            .navigationBarTitleDisplayMode(.inline)
            .confirmationDialog("Disconnect from VPN?", isPresented: $showDisconnectConfirm,
                                titleVisibility: .visible) {
                Button("Disconnect", role: .destructive) { vpn.disconnect() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("Your traffic will no longer be routed through your home network.")
            }
        }
    }

    private func formatBytes(_ bytes: Int64) -> String {
        let kb = Double(bytes) / 1_024
        let mb = kb / 1_024
        let gb = mb / 1_024
        if gb >= 1   { return String(format: "%.2f GB", gb) }
        if mb >= 1   { return String(format: "%.1f MB", mb) }
        if kb >= 1   { return String(format: "%.0f KB", kb) }
        return "\(bytes) B"
    }
}

// MARK: - StatCell

private struct StatCell: View {
    let icon:      String
    let iconColor: Color
    let label:     String
    let value:     String

    var body: some View {
        VStack(spacing: 6) {
            Image(systemName: icon)
                .font(.title2)
                .foregroundStyle(iconColor)
            Text(value)
                .font(.headline.monospaced())
            Text(label)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
    }
}
