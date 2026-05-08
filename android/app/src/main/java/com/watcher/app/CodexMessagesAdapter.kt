package com.watcher.app

import android.graphics.Color
import android.view.Gravity
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import com.google.android.material.card.MaterialCardView

class CodexMessagesAdapter : ListAdapter<CodexThreadMessageV2, CodexMessagesAdapter.MessageViewHolder>(DiffCallback) {

    init {
        setHasStableIds(true)
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): MessageViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_codex_message, parent, false)
        return MessageViewHolder(view)
    }

    override fun onBindViewHolder(holder: MessageViewHolder, position: Int) {
        holder.bind(getItem(position))
    }

    override fun getItemId(position: Int): Long {
        val item = getItem(position)
        return messageStableKey(item).hashCode().toLong()
    }

    private object DiffCallback : DiffUtil.ItemCallback<CodexThreadMessageV2>() {
        override fun areItemsTheSame(oldItem: CodexThreadMessageV2, newItem: CodexThreadMessageV2): Boolean {
            return messageStableKey(oldItem) == messageStableKey(newItem)
        }

        override fun areContentsTheSame(oldItem: CodexThreadMessageV2, newItem: CodexThreadMessageV2): Boolean {
            return oldItem == newItem
        }
    }

    companion object {
        private fun messageStableKey(message: CodexThreadMessageV2): String {
            if (message.messageId.isNotBlank()) {
                return message.messageId
            }
            return listOf(message.turnId, message.role, message.occurredAt, message.text.take(96)).joinToString("|")
        }
    }

    class MessageViewHolder(itemView: View) : RecyclerView.ViewHolder(itemView) {
        private val cardView: MaterialCardView = itemView.findViewById(R.id.codexMessageCard)
        private val roleView: TextView = itemView.findViewById(R.id.codexMessageRole)
        private val bodyView: TextView = itemView.findViewById(R.id.codexMessageBody)
        private val metaView: TextView = itemView.findViewById(R.id.codexMessageMeta)

        fun bind(message: CodexThreadMessageV2) {
            val role = message.role.lowercase()
            val isUser = role == "user"
            val isAssistant = role == "assistant"
            val isPilot = message.turnId.startsWith("pilotchat")
            val isCcMimo = message.turnId.startsWith("ccsess")
            roleView.text = when {
                isUser -> "You"
                isAssistant && isCcMimo -> "CC MiMo"
                isAssistant && isPilot -> "MiMo"
                isAssistant -> "Codex"
                else -> message.role.ifBlank { "message" }.replaceFirstChar { it.uppercase() }
            }
            bodyView.text = message.text
            bodyView.maxWidth = (itemView.resources.displayMetrics.widthPixels * 0.68f).toInt()
            metaView.text = buildString {
                if (message.occurredAt.isNotBlank()) {
                    append(itemView.context.displayTime(message.occurredAt).ifBlank { message.occurredAt })
                } else {
                    append(message.turnId.ifBlank { "message" })
                }
                if (message.phase.isNotBlank()) {
                    append(" · ")
                    append(message.phase)
                }
            }

            val background = when {
                isUser -> "#D9E5F7"
                isAssistant -> "#FFF9EE"
                else -> "#F4F4F5"
            }
            val roleColor = when {
                isUser -> "#1D4ED8"
                isAssistant -> "#8A4B14"
                else -> "#475569"
            }
            val metaColor = when {
                isUser -> "#315C95"
                isAssistant -> "#7C5B33"
                else -> "#64748B"
            }
            roleView.setTextColor(Color.parseColor(roleColor))
            metaView.setTextColor(Color.parseColor(metaColor))
            cardView.setCardBackgroundColor(Color.parseColor(background))

            val gravity = when {
                isUser -> Gravity.END
                isAssistant -> Gravity.START
                else -> Gravity.CENTER_HORIZONTAL
            }
            (cardView.layoutParams as LinearLayout.LayoutParams).gravity = gravity
            (metaView.layoutParams as LinearLayout.LayoutParams).gravity = gravity
        }
    }
}
