// Copyright (c) 2026 d991d. All rights reserved.

import SwiftUI
import HometunnelCore

struct ConnectView: View {
    @EnvironmentObject var vpn: VPNManager

    @State private var inviteLink = ""
    @State private var errorMessage: String?
    @State private var showError = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 36) {

                    // ── Logo ─────────────────────────────────────────────────
                    VStack(spacing: 12) {
                        ZStack {
                            Circle()
                                .fill(.tint.opacity(0.12))
                                .frame(width: 100, height: 100)
                            Image(systemName: "shield.lefthalf.filled")
                                .font(.system(size: 52, weight: .medium))
                                .foregroundStyle(.tint)
                        }

                        Text("HomeTunnel")
                            .font(.largeTitle.bold())

                        Text("Personal VPN — connect to your home network")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                    }
                    .padding(.top, 40)

                    // ── Invite link input ────────────────────────────────────
                    VStack(alignment: .leading, spacing: 10) {
                        Label("Invite Link", systemImage: "link")
                            .font(.headline)

                        ZStack(alignment: .trailing) {
                            TextField(
                                "hometunnel://connect?server=…&token=…",
                                text: $inviteLink,
                                axis: .vertical
                            )
                            .lineLimit(3)
                            .textFieldStyle(.roundedBorder)
                            .autocorrectionDisabled()
                            .textInputAutocapitalization(.never)
                            .keyboardType(.URL)
                            .padding(.trailing, 44)

                            // Paste button
                            Button {
                                if let str = UIPasteboard.general.string { inviteLink = str }
                            } label: {
                                Image(systemName: "doc.on.clipboard")
                                    .padding(10)
                                    .background(.secondary.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
                            }
                            .padding(.trailing, 4)
                        }

                        if !inviteLink.isEmpty {
                            linkPreview
                        }
                    }
                    .padding(.horizontal)

                    // ── Connect button ───────────────────────────────────────
                    Button(action: connect) {
                        Label("Connect", systemImage: "arrow.right.circle.fill")
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 14)
                            .font(.headline)
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .disabled(inviteLink.trimmingCharacters(in: .whitespaces).isEmpty)
                    .padding(.horizontal)
                }
                .padding(.bottom, 40)
            }
            .navigationTitle("")
            .navigationBarTitleDisplayMode(.inline)
            .alert("Cannot Connect", isPresented: $showError, presenting: errorMessage) { _ in
                Button("OK", role: .cancel) {}
            } message: { msg in
                Text(msg)
            }
        }
        .onAppear {
            // Pre-fill from last used link
            if inviteLink.isEmpty { inviteLink = vpn.lastInviteLink }
        }
    }

    // ── Inline link preview ───────────────────────────────────────────────────

    @ViewBuilder
    private var linkPreview: some View {
        if let p = MobileParseInviteLink(inviteLink), p.error.isEmpty {
            HStack(spacing: 6) {
                Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Server: \(p.serverAddr)").font(.caption).foregroundStyle(.secondary)
                }
            }
            .padding(8)
            .background(.green.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
        } else if !inviteLink.isEmpty {
            HStack(spacing: 6) {
                Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                Text("Invalid invite link format").font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    // ─────────────────────────────────────────────────────────────────────────

    private func connect() {
        let link = inviteLink.trimmingCharacters(in: .whitespaces)
        guard let p = MobileParseInviteLink(link) else {
            show(error: "Could not parse invite link.")
            return
        }
        if !p.error.isEmpty { show(error: p.error); return }

        vpn.lastInviteLink = link
        vpn.connect(serverAddr: p.serverAddr, token: p.token)
    }

    private func show(error: String) {
        errorMessage = error
        showError = true
    }
}
