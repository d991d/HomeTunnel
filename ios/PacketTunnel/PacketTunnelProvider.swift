// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Network Extension (Packet Tunnel Provider)
//
// Runs in a separate OS process from the main app.
// Owns the Go VPN engine and handles all packet I/O.

import NetworkExtension
import HometunnelCore   // gomobile-generated xcframework
import os.log

private let log = OSLog(subsystem: "io.github.d991d.hometunnel.PacketTunnel", category: "tunnel")

// MARK: - PacketTunnelProvider

class PacketTunnelProvider: NEPacketTunnelProvider {

    private var engine:          MobileEngine?
    private var startCompletion: ((Error?) -> Void)?

    private let appGroupID = "group.io.github.d991d.hometunnel"
    private var sharedDefaults: UserDefaults? { UserDefaults(suiteName: appGroupID) }

    // ── Tunnel lifecycle ──────────────────────────────────────────────────────

    override func startTunnel(options: [String: NSObject]?,
                              completionHandler: @escaping (Error?) -> Void) {
        os_log("startTunnel called", log: log)

        guard
            let proto  = protocolConfiguration as? NETunnelProviderProtocol,
            let config = proto.providerConfiguration,
            let addr   = config["serverAddr"] as? String,
            let token  = config["token"]      as? String
        else {
            completionHandler(htError("Missing VPN configuration"))
            return
        }

        // Clear previous stats
        sharedDefaults?.removeObject(forKey: "virtualIP")
        sharedDefaults?.removeObject(forKey: "bytesIn")
        sharedDefaults?.removeObject(forKey: "bytesOut")

        let eng = MobileNewEngine()!
        self.engine = eng
        self.startCompletion = completionHandler

        let listener = TunnelEventListener(provider: self)
        eng.setListener(listener)

        os_log("Connecting to %{public}@", log: log, addr)
        eng.connect(addr, token: token, displayName: deviceName())
    }

    override func stopTunnel(with reason: NEProviderStopReason,
                             completionHandler: @escaping () -> Void) {
        os_log("stopTunnel: reason=%d", log: log, reason.rawValue)
        engine?.disconnect()
        engine = nil
        completionHandler()
    }

    // ── Called by TunnelEventListener on successful handshake ─────────────────

    func didConnect(virtualIP: String, mtu: Int) {
        os_log("Handshake complete — virtual IP %{public}@, MTU %d", log: log, virtualIP, mtu)

        sharedDefaults?.set(virtualIP, forKey: "virtualIP")

        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "10.8.0.1")

        // IPv4 — full tunnel (all traffic goes through VPN)
        let ipv4 = NEIPv4Settings(addresses: [virtualIP], subnetMasks: ["255.255.255.0"])
        ipv4.includedRoutes = [NEIPv4Route.default()]
        settings.ipv4Settings = ipv4

        settings.mtu = NSNumber(value: mtu > 0 ? mtu : 1380)

        let dns = NEDNSSettings(servers: ["1.1.1.1", "8.8.8.8"])
        dns.matchDomains = [""] // route all DNS queries through the VPN
        settings.dnsSettings = dns

        setTunnelNetworkSettings(settings) { [weak self] error in
            guard let self else { return }
            if let error {
                os_log("setTunnelNetworkSettings error: %{public}@", log: log, error.localizedDescription)
                self.startCompletion?(error)
                self.startCompletion = nil
                return
            }
            self.startCompletion?(nil)
            self.startCompletion = nil
            self.startPacketForwarding()
        }
    }

    func didError(_ message: String) {
        os_log("Engine error: %{public}@", log: log, message)
        startCompletion?(htError(message))
        startCompletion = nil
    }

    func didUpdateStats(bytesIn: Int64, bytesOut: Int64) {
        sharedDefaults?.set(bytesIn,  forKey: "bytesIn")
        sharedDefaults?.set(bytesOut, forKey: "bytesOut")
    }

    // ── Packet forwarding ─────────────────────────────────────────────────────

    private func startPacketForwarding() {
        guard let eng = engine else { return }

        // Outbound: TUN → Go engine → server
        readOutboundPackets()

        // Inbound: server → Go engine → TUN
        // readPacket() blocks until a packet arrives (or engine stops).
        Thread.detachNewThread { [weak self] in
            guard let self, let eng = self.engine else { return }
            while let pkt = eng.readPacket() as Data? {
                let proto = NSNumber(value: self.ipVersion(of: pkt))
                self.packetFlow.writePackets([pkt], withProtocols: [proto])
            }
            os_log("Inbound forwarding loop exited", log: log)
        }
    }

    /// Reads the next batch of outbound packets from the TUN interface.
    /// Recurses to keep the read loop alive.
    private func readOutboundPackets() {
        packetFlow.readPackets { [weak self] packets, _ in
            guard let self, let eng = self.engine else { return }
            for pkt in packets {
                let err = eng.writePacket(pkt)
                if !err.isEmpty {
                    os_log("writePacket error: %{public}@", log: log, err)
                }
            }
            self.readOutboundPackets()
        }
    }

    // ── Helpers ───────────────────────────────────────────────────────────────

    /// Returns AF_INET (2) or AF_INET6 (30) by inspecting the IP version nibble.
    private func ipVersion(of pkt: Data) -> Int32 {
        guard let first = pkt.first else { return AF_INET }
        return (first >> 4) == 6 ? AF_INET6 : AF_INET
    }

    private func deviceName() -> String {
        // UIDevice is not available in Network Extensions (no UIKit linkage).
        // Use uname() syscall which works in any process.
        var systemInfo = utsname()
        uname(&systemInfo)
        let deviceModel = withUnsafePointer(to: &systemInfo.nodename) {
            $0.withMemoryRebound(to: CChar.self, capacity: 256) {
                String(cString: $0)
            }
        }
        return deviceModel.isEmpty ? "iPhone" : deviceModel
    }

    private func htError(_ msg: String) -> NSError {
        NSError(domain: "io.github.d991d.hometunnel",
                code: -1,
                userInfo: [NSLocalizedDescriptionKey: msg])
    }
}

// MARK: - TunnelEventListener

/// Bridges Go engine JSON events to PacketTunnelProvider callbacks.
final class TunnelEventListener: NSObject, MobileEventListenerProtocol {
    weak var provider: PacketTunnelProvider?
    private var waitingForConnect = true

    init(provider: PacketTunnelProvider) {
        self.provider = provider
    }

    func onEvent(_ payload: String!) {
        guard
            let data = payload?.data(using: .utf8),
            let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            let event = json["event"] as? String
        else { return }

        switch event {
        case "status":
            let state = json["state"] as? String ?? ""
            switch state {
            case "connected":
                let virtualIP = json["virtual_ip"] as? String ?? ""
                let mtu       = json["mtu"]        as? Int    ?? 1380
                if waitingForConnect {
                    waitingForConnect = false
                    provider?.didConnect(virtualIP: virtualIP, mtu: mtu)
                }
            case "disconnected" where !waitingForConnect:
                // Extension will be stopped by the system
                break
            default:
                break
            }

        case "stats":
            let bytesIn  = json["bytes_in"]  as? Int64 ?? 0
            let bytesOut = json["bytes_out"] as? Int64 ?? 0
            provider?.didUpdateStats(bytesIn: bytesIn, bytesOut: bytesOut)

        case "error":
            let msg = json["message"] as? String ?? "Unknown engine error"
            if waitingForConnect {
                waitingForConnect = false
                provider?.didError(msg)
            }

        case "log":
            let msg = json["message"] as? String ?? ""
            os_log("Engine: %{public}@", log: OSLog(subsystem: "io.github.d991d.hometunnel.PacketTunnel", category: "engine"), msg)

        default:
            break
        }
    }
}
