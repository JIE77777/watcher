package com.watcher.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView

class CodexSessionsAdapter(
    private val onClick: (CodexThreadListItemV2) -> Unit
) : ListAdapter<CodexThreadListItemV2, CodexSessionsAdapter.SessionViewHolder>(DiffCallback) {

    init {
        setHasStableIds(true)
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): SessionViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_codex_session, parent, false)
        return SessionViewHolder(view, onClick)
    }

    override fun onBindViewHolder(holder: SessionViewHolder, position: Int) {
        holder.bind(getItem(position))
    }

    override fun getItemId(position: Int): Long = getItem(position).thread.threadId.hashCode().toLong()

    private object DiffCallback : DiffUtil.ItemCallback<CodexThreadListItemV2>() {
        override fun areItemsTheSame(oldItem: CodexThreadListItemV2, newItem: CodexThreadListItemV2): Boolean {
            return oldItem.thread.threadId == newItem.thread.threadId
        }

        override fun areContentsTheSame(oldItem: CodexThreadListItemV2, newItem: CodexThreadListItemV2): Boolean {
            return oldItem == newItem
        }
    }

    class SessionViewHolder(
        itemView: View,
        private val onClick: (CodexThreadListItemV2) -> Unit
    ) : RecyclerView.ViewHolder(itemView) {
        private val titleView: TextView = itemView.findViewById(R.id.codexSessionTitle)
        private val summaryView: TextView = itemView.findViewById(R.id.codexSessionSummary)
        private val metaView: TextView = itemView.findViewById(R.id.codexSessionMeta)
        private var current: CodexThreadListItemV2? = null

        init {
            itemView.setOnClickListener {
                current?.let(onClick)
            }
        }

        fun bind(item: CodexThreadListItemV2) {
            current = item
            val thread = item.thread
            titleView.text = thread.name.ifBlank { thread.threadId }
            summaryView.text = thread.preview.ifBlank {
                if (thread.cwd.isBlank()) {
                    "No message preview yet."
                } else {
                    thread.cwd
                }
            }
            metaView.text = buildString {
                append(thread.source.ifBlank { "unknown" })
                append(" | ")
                append(displayThreadState(item))
                if (thread.status.activeFlags.isNotEmpty()) {
                    append(" · ")
                    append(thread.status.activeFlags.joinToString(" "))
                }
                if (item.overlay?.appManaged == true) {
                    append(" | app")
                }
                if (item.overlay?.desktopAttached == true) {
                    append(" | desktop")
                }
                if (thread.status.type == "active") {
                    append(" | busy")
                }
                item.operation?.let { operation ->
                    append("\nlatest: ")
                    append(operation.kind)
                    append(" · ")
                    append(operation.status)
                    if (operation.lastError.isNotBlank()) {
                        append(" · ")
                        append(operation.lastError.take(80))
                    }
                }
                if (thread.updatedAt.isNotBlank()) {
                    append("\n")
                    append(thread.updatedAt)
                }
                if (thread.agentNickname.isNotBlank()) {
                    append("\n")
                    append(thread.agentNickname)
                    if (thread.agentRole.isNotBlank()) {
                        append(" · ")
                        append(thread.agentRole)
                    }
                }
            }
        }

        private fun displayThreadState(item: CodexThreadListItemV2): String {
            val operation = item.operation
            if (operation != null) {
                return when (operation.status) {
                    "accepted" -> "accepted"
                    "queued" -> "queued"
                    "running" -> "running"
                    "waiting_user_input" -> "waiting"
                    "failed" -> "failed"
                    "interrupted" -> "interrupted"
                    else -> item.thread.status.type.ifBlank { operation.status.ifBlank { "unknown" } }
                }
            }
            return item.thread.status.type.ifBlank { "unknown" }
        }
    }
}
