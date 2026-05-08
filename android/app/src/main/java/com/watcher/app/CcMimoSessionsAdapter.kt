package com.watcher.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

class CcMimoSessionsAdapter(
    private val onClick: (CcMimoSession) -> Unit,
    private val onLongClick: ((CcMimoSession) -> Unit)? = null
) : RecyclerView.Adapter<CcMimoSessionsAdapter.SessionViewHolder>() {
    private val items = mutableListOf<CcMimoSession>()

    fun submitList(sessions: List<CcMimoSession>) {
        items.clear()
        items.addAll(sessions)
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): SessionViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_cc_mimo_session, parent, false)
        return SessionViewHolder(view, onClick, onLongClick)
    }

    override fun onBindViewHolder(holder: SessionViewHolder, position: Int) {
        holder.bind(items[position])
    }

    override fun getItemCount(): Int = items.size

    class SessionViewHolder(
        itemView: View,
        private val onClick: (CcMimoSession) -> Unit,
        private val onLongClick: ((CcMimoSession) -> Unit)? = null
    ) : RecyclerView.ViewHolder(itemView) {
        private val titleView: TextView = itemView.findViewById(R.id.ccMimoSessionTitle)
        private val summaryView: TextView = itemView.findViewById(R.id.ccMimoSessionSummary)
        private val metaView: TextView = itemView.findViewById(R.id.ccMimoSessionMeta)
        private var current: CcMimoSession? = null

        init {
            itemView.setOnClickListener {
                current?.let(onClick)
            }
            itemView.setOnLongClickListener {
                val item = current
                if (item != null && onLongClick != null) {
                    onLongClick.invoke(item)
                    true
                } else {
                    false
                }
            }
        }

        fun bind(item: CcMimoSession) {
            current = item
            titleView.text = item.title.ifBlank { item.sessionId }
            val last = item.messages.lastOrNull { it.role == "assistant" || it.role == "user" }
            summaryView.text = last?.text?.ifBlank { null } ?: item.cwd.ifBlank { "No turns yet." }
            metaView.text = buildString {
                append(item.status.ifBlank { "idle" })
                append(" · ")
                append(item.permissionMode.ifBlank { "bypassPermissions" })
                append(" · ")
                append(item.model.ifBlank { "mimo-v2.5-pro" })
                if (item.activeOperationId.isNotBlank()) {
                    append("\nactive: ")
                    append(item.activeOperationId)
                }
                if (item.lastError.isNotBlank()) {
                    append("\n")
                    append(item.lastError.take(120))
                }
                if (item.updatedAt.isNotBlank()) {
                    append("\n")
                    append(item.updatedAt)
                }
            }
        }
    }
}
