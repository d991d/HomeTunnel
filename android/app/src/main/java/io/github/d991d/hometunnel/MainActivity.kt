// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Main Activity
package io.github.d991d.hometunnel

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.net.VpnService
import android.os.Bundle
import android.util.Log
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import io.github.d991d.hometunnel.databinding.ActivityMainBinding
import mobile.Mobile
import org.json.JSONObject

/**
 * MainActivity hosts three fragments:
 *   - ConnectFragment  — enter invite link / connect
 *   - ConnectingFragment — spinner while connecting
 *   - ConnectedFragment  — stats + disconnect button
 *
 * It also owns the VPN permission request flow and receives broadcast events
 * from HomeTunnelVpnService to drive UI state transitions.
 */
class MainActivity : AppCompatActivity() {

    companion object {
        private const val TAG = "HomeTunnelMain"
    }

    private lateinit var binding: ActivityMainBinding

    // State passed to the VPN service on connect
    private var pendingServerAddr = ""
    private var pendingToken      = ""
    private var pendingName       = ""

    // VPN permission launcher
    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == RESULT_OK) {
            launchVpnService()
        } else {
            showFragment(ConnectFragment.newInstance("VPN permission denied"))
        }
    }

    // Receive events from the background service
    private val eventReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            val json = intent.getStringExtra(HomeTunnelVpnService.EXTRA_EVENT_JSON) ?: return
            handleEvent(json)
        }
    }

    // ── lifecycle ──────────────────────────────────────────────────────────────

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        if (savedInstanceState == null) {
            showFragment(ConnectFragment.newInstance())
        }

        // Handle hometunnel:// deep-link from intent
        intent?.let { handleIncomingIntent(it) }

        Log.i(TAG, "HomeTunnel ${Mobile.version()} by ${Mobile.author()}")
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIncomingIntent(intent)
    }

    override fun onResume() {
        super.onResume()
        registerReceiver(
            eventReceiver,
            IntentFilter(HomeTunnelVpnService.BROADCAST_EVENT),
            RECEIVER_NOT_EXPORTED
        )
    }

    override fun onPause() {
        super.onPause()
        unregisterReceiver(eventReceiver)
    }

    // ── deep-link handling ────────────────────────────────────────────────────

    private fun handleIncomingIntent(intent: Intent) {
        val data = intent.data ?: return
        val params = Mobile.parseInviteLink(data.toString())
        if (params.error.isNotEmpty()) {
            Log.w(TAG, "invalid invite link: ${params.error}")
            return
        }
        // Pre-fill the connect fragment with link details
        val frag = ConnectFragment.newInstance(
            serverAddr = params.serverAddr,
            token      = params.token
        )
        showFragment(frag)
    }

    // ── connect flow ──────────────────────────────────────────────────────────

    /**
     * Called by ConnectFragment when the user taps Connect.
     * Checks VPN permission first, then starts the service.
     */
    fun requestConnect(serverAddr: String, token: String, displayName: String) {
        pendingServerAddr = serverAddr
        pendingToken      = token
        pendingName       = displayName

        val permIntent = VpnService.prepare(this)
        if (permIntent != null) {
            vpnPermissionLauncher.launch(permIntent)
        } else {
            launchVpnService()
        }
    }

    private fun launchVpnService() {
        showFragment(ConnectingFragment.newInstance())
        val intent = Intent(this, HomeTunnelVpnService::class.java).apply {
            action = HomeTunnelVpnService.ACTION_CONNECT
            putExtra(HomeTunnelVpnService.EXTRA_SERVER_ADDR,  pendingServerAddr)
            putExtra(HomeTunnelVpnService.EXTRA_TOKEN,        pendingToken)
            putExtra(HomeTunnelVpnService.EXTRA_DISPLAY_NAME, pendingName)
        }
        startForegroundService(intent)
    }

    fun requestDisconnect() {
        val intent = Intent(this, HomeTunnelVpnService::class.java).apply {
            action = HomeTunnelVpnService.ACTION_DISCONNECT
        }
        startService(intent)
        showFragment(ConnectFragment.newInstance())
    }

    // ── event handling ────────────────────────────────────────────────────────

    private fun handleEvent(jsonStr: String) {
        try {
            val obj = JSONObject(jsonStr)
            when (obj.optString("event")) {
                "status" -> when (obj.optString("state")) {
                    "connected"    -> showFragment(ConnectedFragment.newInstance(
                        obj.optString("virtual_ip"),
                        obj.optInt("mtu", 1380)
                    ))
                    "reconnecting" -> showFragment(ConnectingFragment.newInstance(reconnecting = true))
                    "disconnected" -> showFragment(ConnectFragment.newInstance())
                }
                "stats" -> {
                    val frag = supportFragmentManager.findFragmentById(R.id.fragment_container)
                    if (frag is ConnectedFragment) {
                        frag.updateStats(
                            bytesIn  = obj.optLong("bytes_in"),
                            bytesOut = obj.optLong("bytes_out")
                        )
                    }
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "handleEvent: $e")
        }
    }

    // ── fragment navigation ───────────────────────────────────────────────────

    private fun showFragment(fragment: androidx.fragment.app.Fragment) {
        supportFragmentManager.beginTransaction()
            .replace(R.id.fragment_container, fragment)
            .commit()
    }
}
