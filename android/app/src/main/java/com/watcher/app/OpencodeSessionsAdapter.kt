package com.watcher.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

data class OpencodeSessionListItem(
    val session: OpencodeSession,
    val latestTurn: OpencodeTurn?,
    val preview: String,
    val pendingPermissionCount: Int = 0,
    val pendingQuestionCount: Int = 0,
    val active: Boolean = false
)

fun OpencodeMirrorSession.toLegacyListItem(): OpencodeSessionListItem {
    val legacy = OpencodeSession(
        sessionId = nativeSessionId,
        title = title,
        repoRoot = repoRoot,
        nativeSessionId = nativeSessionId,
        status = status,
        activeTurnId = if (status == "busy") nativeSessionId else "",
        driver = "mirror",
        configJson = null,
        createdAt = createdAt,
        updatedAt = updatedAt
    )
    return OpencodeSessionListItem(
        session = legacy,
        latestTurn = null,
        preview = statusJson?.optString("message").orEmpty(),
        active = status == "busy"
    )
}

fun OpencodeMirrorSession.toListEntry(): OpencodeMirrorSessionEntry {
    return OpencodeMirrorSessionEntry(
        session = this,
        title = title.takeUnless { it.isBlank() || it == "Opencode Session" }.orEmpty(),
        summary = statusJson?.optString("message").orEmpty(),
        detail = repoRoot.substringAfterLast('/'),
        status = status,
        lastRole = "",
        messageCount = 0,
        pendingQuestionCount = 0,
        active = status == "busy",
        updatedAt = updatedAt.ifBlank { syncedAt.ifBlank { createdAt } }
    )
}

class OpencodeSessionsAdapter(
    private val onClick: (OpencodeMirrorSession) -> Unit
) : RecyclerView.Adapter<OpencodeSessionsAdapter.SessionViewHolder>() {
    private val items = mutableListOf<OpencodeMirrorSessionEntry>()

    fun submitList(sessions: List<OpencodeMirrorSessionEntry>) {
        items.clear()
        items.addAll(sessions)
        notifyDataSetChanged()
    }

    fun currentItems(): List<OpencodeMirrorSessionEntry> = items.toList()

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): SessionViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_opencode_session, parent, false)
        return SessionViewHolder(view, onClick)
    }

    override fun onBindViewHolder(holder: SessionViewHolder, position: Int) {
        holder.bind(items[position])
    }

    override fun getItemCount(): Int = items.size

    class SessionViewHolder(
        itemView: View,
        private val onClick: (OpencodeMirrorSession) -> Unit
    ) : RecyclerView.ViewHolder(itemView) {
        private val titleView: TextView = itemView.findViewById(R.id.opencodeSessionItemTitle)
        private val stateView: TextView = itemView.findViewById(R.id.opencodeSessionItemState)
        private val summaryView: TextView = itemView.findViewById(R.id.opencodeSessionItemSummary)
        private val metaView: TextView = itemView.findViewById(R.id.opencodeSessionItemMeta)
        private var current: OpencodeMirrorSessionEntry? = null

        init {
            itemView.setOnClickListener {
                current?.session?.let(onClick)
            }
        }

        fun bind(item: OpencodeMirrorSessionEntry) {
            current = item
            val session = item.session
            val displayTitle = displayTitle(item)
            titleView.text = displayTitle
            stateView.text = sessionStateLabel(item)
            stateView.setTextColor(stateColor(item))
            summaryView.text = item.summary
                .takeIf { it.isNotBlank() && it != displayTitle }
                ?: if (item.messageCount <= 0) "新会话，发送第一条消息开始。" else "打开查看最近对话。"
            metaView.text = buildString {
                val details = mutableListOf<String>()
                if (item.pendingQuestionCount > 0) {
                    details += "等待输入 ${item.pendingQuestionCount}"
                }
                if (item.messageCount > 0) {
                    details += "${item.messageCount} 条消息"
                }
                if (item.lastRole.isNotBlank()) {
                    details += roleLabel(item.lastRole)
                }
                item.detail.takeIf { it.isNotBlank() }?.let { details += it }
                append(details.joinToString(" · "))
                val time = item.updatedAt.ifBlank { session.updatedAt }
                if (time.isNotBlank()) {
                    if (isNotEmpty()) append("\n")
                    append(compactTime(time))
                }
            }
        }

        private fun displayTitle(item: OpencodeMirrorSessionEntry): String {
            val sessionTitle = item.session.title.trim().takeUnless {
                it.isBlank() || it == "Opencode Session" || it.startsWith("New session - ")
            }
            return item.title.trim().takeIf { it.isNotBlank() }
                ?: sessionTitle
                ?: item.session.repoRoot.substringAfterLast('/').takeIf { it.isNotBlank() }
                ?: "Opencode 会话"
        }

        private fun sessionStateLabel(item: OpencodeMirrorSessionEntry): String {
            return when {
                item.pendingQuestionCount > 0 -> "等待"
                item.active || item.status == "busy" || item.status == "retry" -> "运行"
                item.status == "failed" -> "失败"
                else -> "可继续"
            }
        }

        private fun stateColor(item: OpencodeMirrorSessionEntry): Int {
            return when {
                item.pendingQuestionCount > 0 -> 0xFF92400E.toInt()
                item.active || item.status == "busy" || item.status == "retry" -> 0xFF1D4ED8.toInt()
                item.status == "failed" -> 0xFFBE123C.toInt()
                else -> 0xFF475569.toInt()
            }
        }

        private fun roleLabel(role: String): String {
            return when (role.lowercase()) {
                "assistant" -> "最近回复"
                "user" -> "最近提问"
                else -> role
            }
        }

        private fun compactTime(value: String): String {
            return value.replace("T", " ").removeSuffix("Z").take(16)
        }
    }
}
