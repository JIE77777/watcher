package com.watcher.app

import android.graphics.Color
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

class ComponentCellsAdapter(
    private val onClick: (ComponentCell) -> Unit
) : RecyclerView.Adapter<ComponentCellsAdapter.CellViewHolder>() {
    private val items = mutableListOf<ComponentCell>()

    fun submitList(cells: List<ComponentCell>) {
        items.clear()
        items.addAll(cells)
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): CellViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_component_cell, parent, false)
        return CellViewHolder(view, onClick)
    }

    override fun onBindViewHolder(holder: CellViewHolder, position: Int) {
        holder.bind(items[position])
    }

    override fun getItemCount(): Int = items.size

    class CellViewHolder(
        itemView: View,
        private val onClick: (ComponentCell) -> Unit
    ) : RecyclerView.ViewHolder(itemView) {
        private val iconView: TextView = itemView.findViewById(R.id.componentIconText)
        private val labelView: TextView = itemView.findViewById(R.id.componentLabelText)
        private val stateView: TextView = itemView.findViewById(R.id.componentStateText)
        private var current: ComponentCell? = null

        init {
            itemView.setOnClickListener {
                current?.let(onClick)
            }
        }

        fun bind(cell: ComponentCell) {
            current = cell
            val context = itemView.context
            iconView.text = cell.icon.ifBlank { "·" }
            labelView.text = cell.label.ifBlank { cell.componentId }
            stateView.text = listOf(stateLabel(context, cell.state), cell.badge)
                .filter { it.isNotBlank() }
                .joinToString(" · ")
            stateView.setTextColor(Color.parseColor(stateColor(cell.state)))
        }

        private fun stateLabel(context: android.content.Context, state: String): String {
            return when (state) {
                "ready" -> context.watcherText("Ready", "Ready")
                "idle" -> context.watcherText("Idle", "Idle")
                "run" -> context.watcherText("Run", "Run")
                "wait" -> context.watcherText("Wait", "Wait")
                "new" -> context.watcherText("New", "New")
                "down" -> context.watcherText("Down", "Down")
                "degraded" -> context.watcherText("Degraded", "Degraded")
                "off" -> context.watcherText("Off", "Off")
                else -> state.ifBlank { context.watcherText("Idle", "Idle") }
            }
        }

        private fun stateColor(state: String): String {
            return when (state) {
                "run" -> "#1D4ED8"
                "wait", "new" -> "#92400E"
                "down", "degraded" -> "#BE123C"
                "off" -> "#94A3B8"
                else -> "#475569"
            }
        }
    }
}
