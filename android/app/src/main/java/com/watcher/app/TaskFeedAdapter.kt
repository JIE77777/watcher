package com.watcher.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

class TaskFeedAdapter(
    private val onClick: (WatcherTaskEvent) -> Unit
) : RecyclerView.Adapter<TaskFeedAdapter.EventViewHolder>() {

    private val items = mutableListOf<WatcherTaskEvent>()

    fun submitList(events: List<WatcherTaskEvent>) {
        items.clear()
        items.addAll(events)
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): EventViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_event, parent, false)
        return EventViewHolder(view, onClick)
    }

    override fun onBindViewHolder(holder: EventViewHolder, position: Int) {
        holder.bind(items[position])
    }

    override fun getItemCount(): Int = items.size

    class EventViewHolder(
        itemView: View,
        private val onClick: (WatcherTaskEvent) -> Unit
    ) : RecyclerView.ViewHolder(itemView) {
        private val titleView: TextView = itemView.findViewById(R.id.itemTitle)
        private val summaryView: TextView = itemView.findViewById(R.id.itemSummary)
        private val occurredAtView: TextView = itemView.findViewById(R.id.itemOccurredAt)
        private var current: WatcherTaskEvent? = null

        init {
            itemView.setOnClickListener {
                current?.let(onClick)
            }
        }

        fun bind(event: WatcherTaskEvent) {
            current = event
            titleView.text = event.displayTitle
            summaryView.text = event.summary.ifBlank { event.body }
            occurredAtView.text = itemView.context.displayTime(event.occurredAt).ifBlank { event.occurredAt }
        }
    }
}
