// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Connecting / Reconnecting screen fragment
package io.github.d991d.hometunnel

import android.os.Bundle
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import androidx.fragment.app.Fragment
import io.github.d991d.hometunnel.databinding.FragmentConnectingBinding

class ConnectingFragment : Fragment() {

    companion object {
        private const val ARG_RECONNECTING = "reconnecting"

        fun newInstance(reconnecting: Boolean = false) = ConnectingFragment().apply {
            arguments = Bundle().apply { putBoolean(ARG_RECONNECTING, reconnecting) }
        }
    }

    private var _binding: FragmentConnectingBinding? = null
    private val binding get() = _binding!!

    override fun onCreateView(
        inflater: LayoutInflater, container: ViewGroup?, savedInstanceState: Bundle?
    ): View {
        _binding = FragmentConnectingBinding.inflate(inflater, container, false)
        return binding.root
    }

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        super.onViewCreated(view, savedInstanceState)
        val reconnecting = arguments?.getBoolean(ARG_RECONNECTING) == true
        binding.tvStatus.text = if (reconnecting) "Reconnecting…" else "Connecting…"
        binding.btnCancel.setOnClickListener {
            (activity as? MainActivity)?.requestDisconnect()
        }
    }

    override fun onDestroyView() { super.onDestroyView(); _binding = null }
}
