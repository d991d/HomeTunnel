// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Connect screen fragment
package io.github.d991d.hometunnel

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Toast
import androidx.fragment.app.Fragment
import io.github.d991d.hometunnel.databinding.FragmentConnectBinding
import mobile.Mobile

class ConnectFragment : Fragment() {

    companion object {
        private const val ARG_SERVER = "server_addr"
        private const val ARG_TOKEN  = "token"
        private const val ARG_ERROR  = "error_msg"

        fun newInstance(
            errorMsg:   String = "",
            serverAddr: String = "",
            token:      String = ""
        ) = ConnectFragment().apply {
            arguments = Bundle().apply {
                putString(ARG_SERVER, serverAddr)
                putString(ARG_TOKEN,  token)
                putString(ARG_ERROR,  errorMsg)
            }
        }
    }

    private var _binding: FragmentConnectBinding? = null
    private val binding get() = _binding!!

    override fun onCreateView(
        inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?
    ): View {
        _binding = FragmentConnectBinding.inflate(inflater, container, false)
        return binding.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)

        // Pre-fill from arguments (deep-link or error pass-back)
        arguments?.getString(ARG_SERVER)?.takeIf { it.isNotEmpty() }?.let {
            binding.etServerAddr.setText(it)
            binding.advancedGroup.visibility = View.VISIBLE
        }
        arguments?.getString(ARG_TOKEN)?.takeIf { it.isNotEmpty() }?.let {
            binding.etToken.setText(it)
            binding.advancedGroup.visibility = View.VISIBLE
        }
        arguments?.getString(ARG_ERROR)?.takeIf { it.isNotEmpty() }?.let {
            binding.tvError.text = it
            binding.tvError.visibility = View.VISIBLE
        }

        // Toggle advanced fields (server addr + token) visibility
        binding.btnAdvanced.setOnClickListener {
            val vis = if (binding.advancedGroup.visibility == View.VISIBLE)
                View.GONE else View.VISIBLE
            binding.advancedGroup.visibility = vis
            binding.btnAdvanced.text =
                if (vis == View.VISIBLE) "▲ Hide advanced" else "▼ Advanced"
        }

        // Paste invite link → auto-fill advanced fields
        binding.btnPasteLink.setOnClickListener {
            val clip = requireContext()
                .getSystemService(android.content.ClipboardManager::class.java)
            val text = clip?.primaryClip?.getItemAt(0)?.text?.toString() ?: ""
            if (text.isNotEmpty()) {
                parseAndFill(text)
            } else {
                Toast.makeText(context, "Clipboard is empty", Toast.LENGTH_SHORT).show()
            }
        }

        binding.etInviteLink.setOnFocusChangeListener { _, hasFocus ->
            if (!hasFocus) {
                val text = binding.etInviteLink.text.toString().trim()
                if (text.isNotEmpty()) parseAndFill(text)
            }
        }

        // Connect button
        binding.btnConnect.setOnClickListener {
            doConnect()
        }
    }

    private fun parseAndFill(link: String) {
        val params = Mobile.parseInviteLink(link)
        if (params.error.isNotEmpty()) {
            binding.tvError.text = "Invalid link: ${params.error}"
            binding.tvError.visibility = View.VISIBLE
            return
        }
        binding.tvError.visibility = View.GONE
        binding.etServerAddr.setText(params.serverAddr)
        binding.etToken.setText(params.token)
        binding.advancedGroup.visibility = View.VISIBLE
        binding.etInviteLink.setText(link)
    }

    private fun doConnect() {
        binding.tvError.visibility = View.GONE

        // Resolve server + token: prefer advanced fields, fall back to invite link
        var serverAddr = binding.etServerAddr.text.toString().trim()
        var token      = binding.etToken.text.toString().trim()

        if ((serverAddr.isEmpty() || token.isEmpty())) {
            val link = binding.etInviteLink.text.toString().trim()
            if (link.isEmpty()) {
                showError("Paste an invite link or fill in the server address and token")
                return
            }
            val params = Mobile.parseInviteLink(link)
            if (params.error.isNotEmpty()) {
                showError("Invalid invite link: ${params.error}")
                return
            }
            serverAddr = params.serverAddr
            token = params.token
        }

        val displayName = binding.etDisplayName.text.toString().trim()
            .ifEmpty { "HomeTunnel User" }

        (activity as? MainActivity)?.requestConnect(serverAddr, token, displayName)
    }

    private fun showError(msg: String) {
        binding.tvError.text = msg
        binding.tvError.visibility = View.VISIBLE
    }

    override fun onDestroyView() {
        super.onDestroyView()
        _binding = null
    }
}
