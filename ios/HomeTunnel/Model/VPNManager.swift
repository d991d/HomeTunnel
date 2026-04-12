// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — VPN state manager (main app process)

import NetworkExtension
import Combine
import Foundation

@MainActor
final class VPNManager: ObservableObject {
    static let shared = VPNManager()

    @Published var status: NEVPNStatus = .disconnected
    @Published var virtualIP: String   = ""
    @Published var bytesIn:   Int64    = 0
    @Published var bytesOut:  Int64    = 0

    // ── Persistence ──────────────────────────────────────────────────────────
    // Last used invite link so the user doesn't have to paste it every time.
    @Published var lastInviteLink: String {
        didSet { UserDefaults.standard.set(lastInviteLink, forKey: "lastInviteLink") }
    }

    private let appGroupID   = "group.io.github.d991d.hometunnel"
    private var sharedDefaults: UserDefaults? { UserDefaults(suiteName: appGroupID) }

    private var manager:    NETunnelProviderManager?
    private var statsTimer: Timer?

    // ─────────────────────────────────────────────────────────────────────────

    private init() {
        lastInviteLink = UserDefaults.standard.string(forKey: "lastInviteLink") ?? ""
        Task { await loadManager() }
        NotificationCenter.default.addObserver(
            self, selector: #selector(vpnStatusChanged(_:)),
            name: .NEVPNStatusDidChange, object: nil
        )
    }

    // ── Manager loading ───────────────────────────────────────────────────────

    func loadManager() async {
        do {
            let managers = try await NETunnelProviderManager.loadAllFromPreferences()
            self.manager = managers.first ?? NETunnelProviderManager()
            self.status  = self.manager?.connection.status ?? .disconnected
        } catch {
            self.manager = NETunnelProviderManager()
        }
    }

    // ── Connect / Disconnect ──────────────────────────────────────────────────

    func connect(serverAddr: String, token: String) {
#if targetEnvironment(simulator)
        // Simulator: Network Extensions don't run — simulate the flow visually
        status = .connecting
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
            self.status    = .connected
            self.virtualIP = "10.8.0.2"
            self.bytesIn   = 0
            self.bytesOut  = 0
            self.startSimulatorStats()
        }
#else
        guard let manager = manager else { return }

        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = "io.github.d991d.hometunnel.PacketTunnel"
        proto.serverAddress            = serverAddr
        proto.providerConfiguration    = ["serverAddr": serverAddr, "token": token]
        proto.disconnectOnSleep        = false

        manager.protocolConfiguration   = proto
        manager.localizedDescription    = "HomeTunnel"
        manager.isEnabled               = true

        Task {
            do {
                try await manager.saveToPreferences()
                try await manager.loadFromPreferences()
                try (manager.connection as! NETunnelProviderSession).startTunnel(options: nil)
            } catch {
                print("[VPNManager] startTunnel error: \(error)")
            }
        }
#endif
    }

    func disconnect() {
#if targetEnvironment(simulator)
        stopSimulatorStats()
        status = .disconnected
        virtualIP = ""; bytesIn = 0; bytesOut = 0
#else
        manager?.connection.stopVPNTunnel()
#endif
    }

#if targetEnvironment(simulator)
    private func startSimulatorStats() {
        statsTimer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            Task { @MainActor in
                self?.bytesIn  += Int64.random(in: 10_000...500_000)
                self?.bytesOut += Int64.random(in: 1_000...50_000)
            }
        }
    }
    private func stopSimulatorStats() {
        statsTimer?.invalidate()
        statsTimer = nil
    }
#endif

    // ── Status observer ───────────────────────────────────────────────────────

    @objc private func vpnStatusChanged(_ notification: Notification) {
        guard let conn = notification.object as? NEVPNConnection else { return }
        let s = conn.status
        status = s
        switch s {
        case .connected:
            startStatsPolling()
        case .disconnected:
            stopStatsPolling()
            virtualIP = ""; bytesIn = 0; bytesOut = 0
        default:
            stopStatsPolling()
        }
    }

    // ── Stats polling (reads from shared App Group UserDefaults) ──────────────

    private func startStatsPolling() {
        pollStats()
        statsTimer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.pollStats() }
        }
    }

    private func stopStatsPolling() {
        statsTimer?.invalidate()
        statsTimer = nil
    }

    private func pollStats() {
        guard let d = sharedDefaults else { return }
        virtualIP = d.string(forKey: "virtualIP") ?? ""
        bytesIn   = (d.object(forKey: "bytesIn")  as? Int64) ?? 0
        bytesOut  = (d.object(forKey: "bytesOut") as? Int64) ?? 0
    }
}
