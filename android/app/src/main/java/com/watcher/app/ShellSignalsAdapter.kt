package com.watcher.app

import android.graphics.Color
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

class ShellSignalsAdapter(
    private val onClick: (ShellSignal) -> Unit
) : RecyclerView.Adapter<ShellSignalsAdapter.SignalViewHolder>() {
    private val items = mutableListOf<ShellSignal>()

    fun submitList(signals: List<ShellSignal>) {
        items.clear()
        items.addAll(signals)
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): SignalViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_shell_signal, parent, false)
        return SignalViewHolder(view, onClick)
    }

    override fun onBindViewHolder(holder: SignalViewHolder, position: Int) {
        holder.bind(items[position])
    }

    override fun getItemCount(): Int = items.size

    class SignalViewHolder(
        itemView: View,
        private val onClick: (ShellSignal) -> Unit
    ) : RecyclerView.ViewHolder(itemView) {
        private val levelView: TextView = itemView.findViewById(R.id.signalLevelText)
        private val metaView: TextView = itemView.findViewById(R.id.signalMetaText)
        private val titleView: TextView = itemView.findViewById(R.id.signalTitleText)
        private val subtitleView: TextView = itemView.findViewById(R.id.signalSubtitleText)
        private var current: ShellSignal? = null

        init {
            itemView.setOnClickListener {
                current?.let(onClick)
            }
        }

        fun bind(signal: ShellSignal) {
            current = signal
            val context = itemView.context
            titleView.text = signal.title.ifBlank { signal.componentId }
            subtitleView.text = signal.subtitle.ifBlank {
                context.watcherText("打开查看", "Open for details")
            }
            metaView.text = context.displayTime(signal.occurredAt).ifBlank { signal.componentId }
            levelView.text = signal.level.ifBlank { "info" }
            val color = when (signal.level) {
                "critical" -> "#9F1239"
                "warning" -> "#92400E"
                "action" -> "#1D4ED8"
                else -> "#475569"
            }
            levelView.setTextColor(Color.parseColor(color))
        }
    }
}
