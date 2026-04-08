// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Connected screen fragment
package io.github.d991d.hometunnel

import android.content.ClipData
import android.content.ClipboardManager
import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Toast
import androidx.fragment.app.Fragment
import io.github.d991d.hometunnel.databinding.FragmentConnectedBinding
import java.util.concurrent.TimeUnit

class ConnectedFragment : Fragment() {

    companion object {
        private const val ARG_IP  = "virtual_ip"
        private const val ARG_MTU = "mtu"

        fun newInstance(virtualIP: String, mtu: Int) = ConnectedFragment().apply {
            arguments = Bundle().apply {
                putString(ARG_IP, virtualIP)
                putInt(ARG_MTU, mtu)
            }
        }
    }

    private var _binding: FragmentConnectedBinding? = null
    private val binding get() = _binding!!

    private var connectedAtMs = System.currentTimeMillis()
    private val durationHandler = android.os.Handler(android.os.Looper.getMainLooper())
    private val durationRunnable = object : Runnable {
        override fun run() {
            updateDuration()
            durationHandler.postDelayed(this, 1000)
        }
    }

    override fun onCreateView(
        inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?
    ): View {
        _binding = FragmentConnectedBinding.inflate(inflater, container, false)
        return binding.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        val ip  = arguments?.getString(ARG_IP) ?: ""
        val mtu = arguments?.getInt(ARG_MTU) ?: 1380

        binding.tvVirtualIp.text  = ip
        binding.tvMtu.text        = "MTU $mtu"

        // Copy IP to clipboard on long-press
        binding.tvVirtualIp.setOnLongClickListener {
            val cm = requireContext().getSystemService(ClipboardManager::class.java)
            cm.setPrimaryClip(ClipData.newPlainText("HomeTunnel IP", ip))
            Toast.makeText(context, "IP copied", Toast.LENGTH_SHORT).show()
            true
        }

        binding.btnDisconnect.setOnClickListener {
            (activity as? MainActivity)?.requestDisconnect()
        }

        connectedAtMs = System.currentTimeMillis()
        durationHandler.post(durationRunnable)
    }

    /** Called by MainActivity when a "stats" event arrives from the service. */
    fun updateStats(bytesIn: Long, bytesOut: Long) {
        _binding ?: return
        binding.tvBytesIn.text  = formatBytes(bytesIn)
        binding.tvBytesOut.text = formatBytes(bytesOut)
    }

    private fun updateDuration() {
        _binding ?: return
        val elapsedMs = System.currentTimeMillis() - connectedAtMs
        val h = TimeUnit.MILLISECONDS.toHours(elapsedMs)
        val m = TimeUnit.MILLISECONDS.toMinutes(elapsedMs) % 60
        val s = TimeUnit.MILLISECONDS.toSeconds(elapsedMs) % 60
        binding.tvDuration.text = String.format("%02d:%02d:%02d", h, m, s)
    }

    private fun formatBytes(bytes: Long): String = when {
        bytes >= 1_000_000_000 -> "%.1f GB".format(bytes / 1e9)
        bytes >= 1_000_000     -> "%.1f MB".format(bytes / 1e6)
        bytes >= 1_000         -> "%.1f KB".format(bytes / 1e3)
        else                   -> "$bytes B"
    }

    override fun onDestroyView() {
        durationHandler.removeCallbacks(durationRunnable)
        super.onDestroyView()
        _binding = null
    }
}
