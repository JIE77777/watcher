package com.watcher.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

class OpencodeEventsAdapter : RecyclerView.Adapter<OpencodeEventsAdapter.EventViewHolder>() {
    private val items = mutableListOf<OpencodeTimelineItem>()

    fun submitList(events: List<OpencodeTimelineItem>) {
        items.clear()
        items.addAll(events)
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): EventViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_opencode_event, parent, false)
        return EventViewHolder(view)
    }

    override fun onBindViewHolder(holder: EventViewHolder, position: Int) {
        holder.bind(items[position])
    }

    override fun getItemCount(): Int = items.size

    class EventViewHolder(itemView: View) : RecyclerView.ViewHolder(itemView) {
        private val titleView: TextView = itemView.findViewById(R.id.opencodeEventTitle)
        private val bodyView: TextView = itemView.findViewById(R.id.opencodeEventBody)
        private val metaView: TextView = itemView.findViewById(R.id.opencodeEventMeta)

        fun bind(event: OpencodeTimelineItem) {
            titleView.text = event.title.ifBlank { event.rawKind.ifBlank { "event" } }
            bodyView.text = eventSummary(event)
            bodyView.visibility = if (bodyView.text.isBlank()) View.GONE else View.VISIBLE
            metaView.text = "#${event.seq} · ${event.type.ifBlank { "event" }} · ${event.occurredAt}"
            titleView.setTextColor(
                when {
                    event.severity == "error" -> 0xFFB91C1C.toInt()
                    event.type == "assistant_text" -> 0xFF111827.toInt()
                    event.type == "tool_call" -> 0xFF475569.toInt()
                    else -> 0xFF334155.toInt()
                }
            )
        }

        private fun eventSummary(event: OpencodeTimelineItem): String {
            return when {
                event.body.isNotBlank() -> event.body
                event.detail.isNotBlank() -> event.detail
                else -> ""
            }.replace("\\/", "/")
        }
    }
}
