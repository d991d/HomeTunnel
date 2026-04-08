// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Android VpnService
package io.github.d991d.hometunnel

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.IBinder
import android.os.ParcelFileDescriptor
import android.util.Log
import androidx.core.app.NotificationCompat
import kotlinx.coroutines.*
import mobile.Engine
import mobile.EventListener
import mobile.Mobile
import org.json.JSONObject
import java.io.FileInputStream
import java.io.FileOutputStream

/**
 * HomeTunnelVpnService runs as a foreground service and owns:
 *   - The Android VPN TUN interface (via VpnService.Builder)
 *   - The Go Engine (UDP transport + crypto via gomobile .aar)
 *   - Two I/O goroutines bridging TUN ↔ Go Engine
 */
class HomeTunnelVpnService : VpnService() {

    companion object {
        private const val TAG = "HomeTunnelVPN"
        private const val NOTIF_CHANNEL = "hometunnel_vpn"
        private const val NOTIF_ID = 1

        // Intent actions
        const val ACTION_CONNECT    = "io.github.d991d.hometunnel.CONNECT"
        const val ACTION_DISCONNECT = "io.github.d991d.hometunnel.DISCONNECT"

        // Intent extras
        const val EXTRA_SERVER_ADDR   = "server_addr"
        const val EXTRA_TOKEN         = "token"
        const val EXTRA_DISPLAY_NAME  = "display_name"

        // Broadcast sent back to the UI
        const val BROADCAST_EVENT = "io.github.d991d.hometunnel.EVENT"
        const val EXTRA_EVENT_JSON = "event_json"
    }

    private var engine: Engine? = null
    private var tunFd: ParcelFileDescriptor? = null
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    // ── lifecycle ──────────────────────────────────────────────────────────────

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        return when (intent?.action) {
            ACTION_CONNECT -> {
                val serverAddr  = intent.getStringExtra(EXTRA_SERVER_ADDR) ?: return START_NOT_STICKY
                val token       = intent.getStringExtra(EXTRA_TOKEN) ?: return START_NOT_STICKY
                val displayName = intent.getStringExtra(EXTRA_DISPLAY_NAME) ?: "HomeTunnel User"
                startForeground(NOTIF_ID, buildNotification("Connecting…"))
                startTunnel(serverAddr, token, displayName)
                START_STICKY
            }
            ACTION_DISCONNECT -> {
                stopTunnel()
                stopSelf()
                START_NOT_STICKY
            }
            else -> START_NOT_STICKY
        }
    }

    override fun onDestroy() {
        stopTunnel()
        serviceScope.cancel()
        super.onDestroy()
    }

    // ── tunnel lifecycle ───────────────────────────────────────────────────────

    private fun startTunnel(serverAddr: String, token: String, displayName: String) {
        stopTunnel() // clean up any previous session

        val eng = Engine()
        engine = eng

        // Register the event listener — bridges Go events to Android broadcasts
        eng.setListener(object : EventListener {
            override fun onEvent(payload: String) {
                handleGoEvent(payload, eng)
            }
        })

        // Start Go connection (non-blocking)
        eng.connect(serverAddr, token, displayName)
    }

    private fun stopTunnel() {
        engine?.disconnect()
        engine = null
        tunFd?.close()
        tunFd = null
    }

    // ── Go event handler ───────────────────────────────────────────────────────

    private fun handleGoEvent(jsonStr: String, eng: Engine) {
        try {
            val obj = JSONObject(jsonStr)
            when (obj.optString("event")) {
                "status" -> {
                    when (obj.optString("state")) {
                        "connected" -> {
                            val virtualIP = obj.optString("virtual_ip")
                            val mtu = obj.optInt("mtu", 1380)
                            // Build TUN interface now that we have VPN params
                            serviceScope.launch {
                                openTun(eng, virtualIP, mtu)
                            }
                            updateNotification("Connected — $virtualIP")
                        }
                        "reconnecting" -> updateNotification("Reconnecting…")
                        "disconnected" -> {
                            tunFd?.close(); tunFd = null
                            updateNotification("Disconnected")
                            stopSelf()
                        }
                    }
                }
                "error" -> Log.w(TAG, "engine error: ${obj.optString("message")}")
                "log"   -> Log.d(TAG, "go: ${obj.optString("message")}")
            }
        } catch (e: Exception) {
            Log.e(TAG, "event parse error: $e")
        }

        // Forward all events to the UI activity via LocalBroadcast
        sendBroadcast(Intent(BROADCAST_EVENT).also {
            it.putExtra(EXTRA_EVENT_JSON, jsonStr)
        })
    }

    // ── TUN interface ──────────────────────────────────────────────────────────

    private fun openTun(eng: Engine, virtualIP: String, mtu: Int) {
        try {
            val builder = Builder()
                .setSession("HomeTunnel")
                .setMtu(mtu)
                .addAddress(virtualIP, 24)      // VPN virtual address
                .addRoute("0.0.0.0", 0)         // Route all IPv4 traffic
                .addDnsServer("1.1.1.1")
                .addDnsServer("8.8.8.8")

            val pfd = builder.establish()
                ?: run { Log.e(TAG, "VpnService.Builder.establish() returned null"); return }

            tunFd = pfd

            // TUN → Go: read IP packets from Android TUN, forward to the Go engine
            serviceScope.launch {
                val input = FileInputStream(pfd.fileDescriptor)
                val buf = ByteArray(mtu + 4)
                while (isActive) {
                    try {
                        val n = input.read(buf)
                        if (n > 0) {
                            val errStr = eng.writePacket(buf.copyOf(n))
                            if (errStr.isNotEmpty()) {
                                Log.w(TAG, "writePacket: $errStr")
                            }
                        }
                    } catch (e: Exception) {
                        if (isActive) Log.e(TAG, "TUN read error: $e")
                        break
                    }
                }
            }

            // Go → TUN: read decrypted IP packets from Go engine, write to TUN
            serviceScope.launch {
                val output = FileOutputStream(pfd.fileDescriptor)
                while (isActive) {
                    try {
                        val pkt = eng.readPacket() ?: break
                        output.write(pkt)
                    } catch (e: Exception) {
                        if (isActive) Log.e(TAG, "TUN write error: $e")
                        break
                    }
                }
            }

            Log.i(TAG, "TUN interface open — virtual IP $virtualIP / MTU $mtu")
        } catch (e: Exception) {
            Log.e(TAG, "openTun failed: $e")
        }
    }

    // ── notifications ──────────────────────────────────────────────────────────

    private fun createNotificationChannel() {
        val ch = NotificationChannel(
            NOTIF_CHANNEL,
            "HomeTunnel VPN",
            NotificationManager.IMPORTANCE_LOW
        ).apply {
            description = "HomeTunnel VPN connection status"
            setShowBadge(false)
        }
        getSystemService(NotificationManager::class.java).createNotificationChannel(ch)
    }

    private fun buildNotification(text: String): Notification {
        val pi = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE
        )
        val disconnectPi = PendingIntent.getService(
            this, 1,
            Intent(this, HomeTunnelVpnService::class.java).apply { action = ACTION_DISCONNECT },
            PendingIntent.FLAG_IMMUTABLE
        )
        return NotificationCompat.Builder(this, NOTIF_CHANNEL)
            .setContentTitle("HomeTunnel")
            .setContentText(text)
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentIntent(pi)
            .setOngoing(true)
            .addAction(android.R.drawable.ic_delete, "Disconnect", disconnectPi)
            .build()
    }

    private fun updateNotification(text: String) {
        val nm = getSystemService(NotificationManager::class.java)
        nm.notify(NOTIF_ID, buildNotification(text))
    }
}
