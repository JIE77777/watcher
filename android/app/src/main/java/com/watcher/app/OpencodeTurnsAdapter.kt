package com.watcher.app

import android.content.res.Resources
import android.graphics.Color
import android.graphics.Typeface
import android.text.SpannableStringBuilder
import android.text.Spanned
import android.text.style.BackgroundColorSpan
import android.text.style.ForegroundColorSpan
import android.text.style.RelativeSizeSpan
import android.text.style.StyleSpan
import android.text.style.TypefaceSpan
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.RecyclerView
import org.json.JSONArray
import org.json.JSONObject
import java.time.Duration
import java.time.Instant

data class OpencodeTurnConversationItem(
    val turn: OpencodeTurn,
    val timeline: List<OpencodeTimelineItem>,
    val pendingPermission: OpencodePermissionRequest?,
    val pendingQuestion: OpencodeQuestionRequest?,
    val worktree: OpencodeWorktree?,
    val latest: Boolean,
    val active: Boolean
)

class OpencodeTurnsAdapter(
    private val onCancel: (OpencodeTurn) -> Unit,
    private val onCopy: (String) -> Unit,
    private val onGrant: (OpencodePermissionRequest) -> Unit,
    private val onDeny: (OpencodePermissionRequest) -> Unit,
    private val onAnswerQuestion: (OpencodeQuestionRequest, List<List<String>>) -> Unit,
    private val onRejectQuestion: (OpencodeQuestionRequest) -> Unit,
    private val onOpenQuestion: (OpencodeQuestionRequest) -> Unit,
    private val onDiscardWorktree: (OpencodeWorktree) -> Unit,
    private val onToggle: (Int) -> Unit = {}
) : RecyclerView.Adapter<OpencodeTurnsAdapter.TurnViewHolder>() {
    private val items = mutableListOf<OpencodeTurnConversationItem>()
    private val expandedTurnIds = mutableSetOf<String>()
    private val detailLevelsByTurnId = mutableMapOf<String, Int>()
    private val userCollapsedTurnIds = mutableSetOf<String>()
    private val markdownCache = object : LinkedHashMap<String, CharSequence>(32, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, CharSequence>?): Boolean {
            return size > 32
        }
    }

    init {
        setHasStableIds(true)
    }

    fun submitList(next: List<OpencodeTurnConversationItem>, preferredExpandedIds: Set<String>, onCommitted: () -> Unit = {}) {
        val old = items.toList()
        val oldExpanded = expandedTurnIds.toSet()
        val oldDetailLevels = detailLevelsByTurnId.toMap()
        val nextTurnIds = next.map { it.turn.turnId }.toSet()
        for (turnId in preferredExpandedIds) {
            if (turnId !in userCollapsedTurnIds) {
                expandedTurnIds += turnId
            }
        }
        expandedTurnIds.retainAll(nextTurnIds)
        detailLevelsByTurnId.keys.retainAll(nextTurnIds)
        userCollapsedTurnIds.retainAll(nextTurnIds)
        val newExpanded = expandedTurnIds.toSet()
        val newDetailLevels = detailLevelsByTurnId.toMap()
        val diff = DiffUtil.calculateDiff(object : DiffUtil.Callback() {
            override fun getOldListSize(): Int = old.size

            override fun getNewListSize(): Int = next.size

            override fun areItemsTheSame(oldItemPosition: Int, newItemPosition: Int): Boolean {
                return old[oldItemPosition].turn.turnId == next[newItemPosition].turn.turnId
            }

            override fun areContentsTheSame(oldItemPosition: Int, newItemPosition: Int): Boolean {
                val oldItem = old[oldItemPosition]
                val newItem = next[newItemPosition]
                val oldIsExpanded = oldItem.turn.turnId in oldExpanded
                val newIsExpanded = newItem.turn.turnId in newExpanded
                val oldDetailLevel = oldDetailLevels[oldItem.turn.turnId] ?: 0
                val newDetailLevel = newDetailLevels[newItem.turn.turnId] ?: 0
                return oldItem == newItem && oldIsExpanded == newIsExpanded && oldDetailLevel == newDetailLevel
            }
        })
        items.clear()
        items.addAll(next)
        diff.dispatchUpdatesTo(this)
        onCommitted()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): TurnViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_opencode_turn, parent, false)
        return TurnViewHolder(view)
    }

    override fun onBindViewHolder(holder: TurnViewHolder, position: Int) {
        val turnId = items[position].turn.turnId
        val expanded = turnId in expandedTurnIds
        holder.bind(items[position], expanded, if (expanded) detailLevelsByTurnId[turnId] ?: 0 else 0)
    }

    override fun getItemCount(): Int = items.size

    override fun getItemId(position: Int): Long {
        return items[position].turn.turnId.hashCode().toLong()
    }

    fun turnIdAt(position: Int): String {
        return items.getOrNull(position)?.turn?.turnId.orEmpty()
    }

    fun positionOfTurnId(turnId: String): Int {
        return items.indexOfFirst { it.turn.turnId == turnId }
    }

    inner class TurnViewHolder(itemView: View) : RecyclerView.ViewHolder(itemView) {
        private val userPrompt: TextView = itemView.findViewById(R.id.opencodeTurnUserPrompt)
        private val root: View = itemView.findViewById(R.id.opencodeTurnRoot)
        private val statusRow: View = itemView.findViewById(R.id.opencodeTurnStatusRow)
        private val statusText: TextView = itemView.findViewById(R.id.opencodeTurnStatusText)
        private val assistantText: TextView = itemView.findViewById(R.id.opencodeTurnAssistantText)
        private val resultText: TextView = itemView.findViewById(R.id.opencodeTurnResultText)
        private val permissionPanel: View = itemView.findViewById(R.id.opencodeTurnPermissionPanel)
        private val permissionText: TextView = itemView.findViewById(R.id.opencodeTurnPermissionText)
        private val grantButton: Button = itemView.findViewById(R.id.opencodeTurnGrantButton)
        private val denyButton: Button = itemView.findViewById(R.id.opencodeTurnDenyButton)
        private val questionPanel: View = itemView.findViewById(R.id.opencodeTurnQuestionPanel)
        private val questionText: TextView = itemView.findViewById(R.id.opencodeTurnQuestionText)
        private val questionOptions: LinearLayout = itemView.findViewById(R.id.opencodeTurnQuestionOptions)
        private val questionMoreButton: Button = itemView.findViewById(R.id.opencodeTurnQuestionMoreButton)
        private val questionRejectButton: Button = itemView.findViewById(R.id.opencodeTurnQuestionRejectButton)
        private val worktreePanel: View = itemView.findViewById(R.id.opencodeTurnWorktreePanel)
        private val worktreeText: TextView = itemView.findViewById(R.id.opencodeTurnWorktreeText)
        private val discardButton: Button = itemView.findViewById(R.id.opencodeTurnDiscardButton)
        private val cancelButton: TextView = itemView.findViewById(R.id.opencodeTurnCancelButton)
        private val copyAction: TextView = itemView.findViewById(R.id.opencodeTurnCopyAction)
        private val metaText: TextView = itemView.findViewById(R.id.opencodeTurnMetaText)
        private val detailsToggleText: TextView = itemView.findViewById(R.id.opencodeTurnDetailsToggleText)
        private val detailsText: TextView = itemView.findViewById(R.id.opencodeTurnDetailsText)
        private val toggleText: TextView = itemView.findViewById(R.id.opencodeTurnToggleText)

        fun bind(item: OpencodeTurnConversationItem, expanded: Boolean, detailLevel: Int) {
            val turn = item.turn
            val assistant = assistantBody(item)
            val result = resultSummary(item)
            val processCount = processEventCount(item)
            root.setBackgroundResource(
                when {
                    item.pendingPermission != null || item.pendingQuestion != null -> R.drawable.bg_opencode_turn_attention
                    item.active -> R.drawable.bg_opencode_turn_active
                    else -> R.drawable.bg_opencode_turn
                }
            )
            userPrompt.text = turn.prompt.ifBlank { "空指令" }
            userPrompt.maxLines = if (expanded) Int.MAX_VALUE else 4
            statusText.text = statusLine(item)
            statusText.setTextColor(Color.parseColor(if (item.active || item.pendingQuestion != null || item.pendingPermission != null) "#334155" else "#64748B"))
            assistantText.text = if (assistant.isBlank()) {
                progressText(item)
            } else {
                renderMarkdown(assistant)
            }
            assistantText.maxLines = if (expanded) Int.MAX_VALUE else if (item.pendingQuestion != null || item.pendingPermission != null) 3 else 5
            resultText.text = result
            resultText.visibility = if (shouldShowResult(item, result)) View.VISIBLE else View.GONE
            metaText.text = metaLine(item)
            metaText.visibility = if (expanded) View.VISIBLE else View.GONE
            detailsText.text = processDetails(item, full = detailLevel >= 2)
            detailsText.maxLines = when (detailLevel) {
                1 -> 18
                2 -> 120
                else -> 1
            }
            detailsText.setTextIsSelectable(detailLevel >= 2)
            detailsToggleText.text = processToggleLine(item, detailLevel)
            detailsToggleText.visibility = if (expanded && processCount > 0) View.VISIBLE else View.GONE
            detailsText.visibility = if (expanded && detailLevel > 0 && processCount > 0) View.VISIBLE else View.GONE
            toggleText.text = if (expanded) "收起" else "详情"

            renderPermission(item.pendingPermission)
            renderQuestion(item.pendingQuestion)
            renderWorktree(item.worktree)
            renderActions(item, expanded, assistant)

            val toggle = View.OnClickListener {
                val id = turn.turnId
                if (expandedTurnIds.contains(id)) {
                    expandedTurnIds -= id
                    detailLevelsByTurnId.remove(id)
                    userCollapsedTurnIds += id
                } else {
                    expandedTurnIds += id
                    userCollapsedTurnIds -= id
                }
                val position = bindingAdapterPosition
                if (position != RecyclerView.NO_POSITION) {
                    notifyItemChanged(position)
                    onToggle(position)
                }
            }
            toggleText.setOnClickListener(toggle)
            statusText.setOnClickListener(toggle)
            statusRow.setOnClickListener(toggle)

            detailsToggleText.setOnClickListener {
                val id = turn.turnId
                val nextLevel = when (detailLevelsByTurnId[id] ?: 0) {
                    0 -> 1
                    1 -> 2
                    else -> 0
                }
                if (nextLevel == 0) {
                    detailLevelsByTurnId.remove(id)
                } else {
                    detailLevelsByTurnId[id] = nextLevel
                }
                val position = bindingAdapterPosition
                if (position != RecyclerView.NO_POSITION) {
                    notifyItemChanged(position)
                }
            }
        }

        private fun renderActions(item: OpencodeTurnConversationItem, expanded: Boolean, assistant: String) {
            val showCancel = item.active && item.turn.status in setOf("accepted", "running")
            val showCopy = expanded && assistant.isNotBlank()

            cancelButton.visibility = if (showCancel) View.VISIBLE else View.GONE
            copyAction.visibility = if (showCopy) View.VISIBLE else View.GONE

            cancelButton.setOnClickListener { onCancel(item.turn) }
            copyAction.setOnClickListener { if (assistant.isNotBlank()) onCopy(assistant) }
        }

        private fun renderPermission(permission: OpencodePermissionRequest?) {
            if (permission == null) {
                permissionPanel.visibility = View.GONE
                return
            }
            permissionPanel.visibility = View.VISIBLE
            permissionText.text = permissionSummary(permission)
            grantButton.setOnClickListener { onGrant(permission) }
            denyButton.setOnClickListener { onDeny(permission) }
        }

        private fun renderQuestion(question: OpencodeQuestionRequest?) {
            questionOptions.removeAllViews()
            if (question == null) {
                questionPanel.visibility = View.GONE
                return
            }
            questionPanel.visibility = View.VISIBLE
            questionText.text = questionSummary(question)
            val quickOptions = quickQuestionOptions(question)
            if (quickOptions.isEmpty()) {
                questionOptions.visibility = View.GONE
            } else {
                questionOptions.visibility = View.VISIBLE
                val horizontal = quickOptions.size <= 2
                questionOptions.orientation = if (horizontal) LinearLayout.HORIZONTAL else LinearLayout.VERTICAL
                for (option in quickOptions.take(4)) {
                    val button = Button(itemView.context).apply {
                        text = option.label
                        setAllCaps(false)
                        minWidth = 0
                        minHeight = dp(40)
                        maxLines = 2
                        textSize = 13f
                        setPadding(dp(10), 0, dp(10), 0)
                        setOnClickListener {
                            onAnswerQuestion(question, listOf(listOf(option.value.ifBlank { option.label })))
                        }
                    }
                    questionOptions.addView(
                        button,
                        if (horizontal) {
                            LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f).apply {
                                marginEnd = dp(6)
                            }
                        } else {
                            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT).apply {
                                bottomMargin = dp(6)
                            }
                        }
                    )
                }
            }
            questionMoreButton.text = if (quickOptions.isEmpty()) "选择" else "更多"
            questionMoreButton.setOnClickListener { onOpenQuestion(question) }
            questionRejectButton.setOnClickListener { onRejectQuestion(question) }
        }

        private fun renderWorktree(worktree: OpencodeWorktree?) {
            if (worktree == null || !worktree.exists) {
                worktreePanel.visibility = View.GONE
                return
            }
            worktreePanel.visibility = View.VISIBLE
            worktreeText.text = buildString {
                append("旧隔离工作区")
                if (worktree.changedFiles.isNotEmpty()) {
                    append(" · ")
                    append(worktree.changedFiles.size)
                    append(" 个文件改动")
                }
                if (worktree.diffStat.isNotBlank()) {
                    append("\n")
                    append(worktree.diffStat)
                }
                if (worktree.changedFiles.isNotEmpty()) {
                    append("\n")
                    append(worktree.changedFiles.take(6).joinToString("\n"))
                    if (worktree.changedFiles.size > 6) {
                        append("\n+")
                        append(worktree.changedFiles.size - 6)
                        append(" 个文件")
                    }
                }
            }
            discardButton.setOnClickListener { onDiscardWorktree(worktree) }
        }
    }

    private fun shouldShowResult(item: OpencodeTurnConversationItem, result: String): Boolean {
        if (result.isBlank()) return false
        return item.turn.status in setOf("failed", "interrupted") || displayError(item.turn.error).isNotBlank()
    }

    private fun assistantBody(item: OpencodeTurnConversationItem): String {
        return item.timeline
            .filter { it.type == "assistant_text" && it.body.isNotBlank() }
            .joinToString("\n\n") { it.body.trim() }
            .trim()
    }

    private fun resultSummary(item: OpencodeTurnConversationItem): String {
        val turn = item.turn
        val error = displayError(turn.error)
        if (error.isNotBlank()) {
            return "失败\n$error"
        }
        val permission = item.pendingPermission
        if (permission != null) {
            return "需要授权\n${permission.kind.ifBlank { "permission" }}"
        }
        val question = item.pendingQuestion
        if (question != null) {
            return "需要选择\n${questionSummary(question).lineSequence().firstOrNull().orEmpty()}"
        }
        val worktree = item.worktree
        if (worktree != null && worktree.exists) {
            return if (worktree.changedFiles.isEmpty()) {
                "结果\n已完成 · 旧隔离工作区"
            } else {
                "结果\n已完成 · ${worktree.changedFiles.size} 个文件改动 · 旧隔离工作区"
            }
        }
        if (turn.status in setOf("accepted", "running")) {
            return "进行中\n${currentStage(item)}"
        }
        val completed = item.timeline.lastOrNull { it.rawKind == "turn.completed" || it.title.contains("completed", true) }
        if (completed?.body?.isNotBlank() == true) {
            return "结果\n${completionBrief(completed.body).ifBlank { completed.body.trim() }}"
        }
        if (turn.status == "completed") {
            return "结果\n已完成。"
        }
        return ""
    }

    private fun progressText(item: OpencodeTurnConversationItem): String {
        val turn = item.turn
        if (item.pendingPermission != null) return "Opencode 正在等待你的授权。"
        if (item.pendingQuestion != null) return "Opencode 正在等待你的选择。"
        val error = displayError(turn.error)
        if (turn.status == "failed") return error.ifBlank { "Opencode 执行失败。" }
        if (turn.status == "interrupted") return error.ifBlank { "这轮已取消。" }
        val last = item.timeline.lastOrNull {
            it.body.isNotBlank() && it.type in setOf("tool_call", "worktree", "lifecycle", "log", "error", "question", "permission", "reasoning")
        }
        if (last != null) {
            if (last.rawKind in setOf("native_session.bound", "native_session.resume", "workspace.ready", "worktree.ready")) {
                return currentStage(item)
            }
            return listOf(last.title, last.body).filter { it.isNotBlank() }.joinToString("\n")
        }
        return when (turn.status) {
            "accepted" -> "已接收，等待 Opencode 开始执行。"
            "running" -> currentStage(item)
            "completed" -> "这轮已完成，但没有 assistant 文本。"
            else -> "等待同步这轮的可读内容。"
        }
    }

    private fun statusLine(item: OpencodeTurnConversationItem): String {
        val status = when {
            item.pendingPermission != null -> "等待权限"
            item.pendingQuestion != null -> "等待选择"
            item.turn.status == "accepted" -> "已接收"
            item.turn.status == "running" -> "运行中"
            item.turn.status == "completed" -> "已完成"
            item.turn.status == "failed" -> "失败"
            item.turn.status == "interrupted" -> "已取消"
            else -> item.turn.status.ifBlank { "未知状态" }
        }
        val brief = statusBrief(item)
        val time = item.timeline.lastOrNull()?.occurredAt
            ?: item.turn.completedAt.ifBlank { item.turn.updatedAt.ifBlank { item.turn.createdAt } }
        val activity = if (time.isBlank() || item.turn.status !in setOf("accepted", "running")) {
            ""
        } else {
            " · 最近活动 ${relativeTime(time)}"
        }
        val active = if (item.active) " · 当前任务" else if (item.latest) " · 最新" else ""
        val meta = compactMetaLine(item)
        return listOf(status, brief, meta).filter { it.isNotBlank() }.joinToString(" · ") + active + activity
    }

    private fun statusBrief(item: OpencodeTurnConversationItem): String {
        val turn = item.turn
        if (item.pendingPermission != null) {
            return item.pendingPermission.kind.ifBlank { "需要授权" }
        }
        if (item.pendingQuestion != null) {
            return questionSummary(item.pendingQuestion).lineSequence().firstOrNull().orEmpty().take(48)
        }
        val worktree = item.worktree
        if (worktree != null && worktree.exists) {
            return if (worktree.changedFiles.isEmpty()) {
                "旧隔离工作区"
            } else {
                "${worktree.changedFiles.size} 个文件改动 · 旧隔离工作区"
            }
        }
        val error = displayError(turn.error)
        if (error.isNotBlank()) {
            return error.lineSequence().firstOrNull().orEmpty().take(48)
        }
        if (turn.status == "completed") {
            val completed = item.timeline.lastOrNull { it.rawKind == "turn.completed" || it.title.contains("completed", true) }
            return completionBrief(completed?.body.orEmpty())
        }
        if (turn.status in setOf("accepted", "running")) {
            return currentStage(item).lineSequence().firstOrNull().orEmpty().take(48)
        }
        return ""
    }

    private fun currentStage(item: OpencodeTurnConversationItem): String {
        if (item.pendingPermission != null) {
            return "等待你的授权。"
        }
        if (item.pendingQuestion != null) {
            return "等待你的选择。"
        }
        val last = item.timeline.lastOrNull()
        if (last == null) {
            return when (item.turn.status) {
                "accepted" -> "已接收，等待 Opencode 开始。"
                "running" -> "Opencode 正在运行。"
                else -> "等待同步最新状态。"
            }
        }
        return when {
            last.rawKind == "native_session.bound" -> "opencode 会话已就绪。"
            last.rawKind == "native_session.resume" -> "正在续用 opencode 会话。"
            last.rawKind == "turn.started" || last.type == "lifecycle" -> "已接收，准备执行。"
            last.rawKind == "workspace.ready" -> "项目工作区已准备。"
            last.rawKind == "worktree.ready" -> "旧隔离工作区已准备。"
            last.type == "tool_call" -> {
                val title = last.title.removePrefix("Tool:").trim()
                val body = last.body.lineSequence().firstOrNull().orEmpty().trim()
                listOf("正在执行工具", title, body).filter { it.isNotBlank() }.joinToString(" · ").take(120)
            }
            last.type == "assistant_text" -> "正在生成或整理回复。"
            last.type == "reasoning" -> last.body.lineSequence().firstOrNull().orEmpty().take(120).ifBlank { "正在思考。" }
            last.type == "question" -> last.body.lineSequence().firstOrNull().orEmpty().take(120).ifBlank { "等待你的选择。" }
            last.type == "error" -> last.body.ifBlank { "执行遇到错误。" }.lineSequence().firstOrNull().orEmpty().take(120)
            else -> {
                val body = last.body.ifBlank { last.title }.lineSequence().firstOrNull().orEmpty().trim()
                body.ifBlank { "Opencode 正在运行。" }.take(120)
            }
        }
    }

    private fun completionBrief(body: String): String {
        val text = body.trim()
        if (text.isBlank()) return ""
        val lower = text.lowercase()
        return when {
            "no file changes" in lower && "clean" in lower -> "无文件改动 · 已清理"
            "no file changes" in lower -> "无文件改动"
            text.startsWith("已完成，项目工作区没有文件改动") -> "无文件改动"
            text.startsWith("项目工作区有") -> text.lineSequence().firstOrNull().orEmpty().take(48)
            text.startsWith("项目工作区当前有") -> text.lineSequence().firstOrNull().orEmpty().take(48)
            "worktree" in lower && "retained" in lower -> "旧隔离工作区"
            else -> text.lineSequence().firstOrNull().orEmpty().take(48)
        }
    }

    private fun metaLine(item: OpencodeTurnConversationItem): String {
        if (item.turn.driver == OPENCODE_NATIVE_HISTORY_DRIVER) {
            val parts = mutableListOf(driverLabel(item.turn.driver))
            val duration = durationText(item.turn.startedAt, item.turn.completedAt)
            if (duration.isNotBlank()) parts += duration
            item.timeline.firstOrNull { it.detail.isNotBlank() }?.detail?.let { parts += it }
            return parts.joinToString(" · ")
        }
        val toolCount = item.timeline.count { it.type == "tool_call" }
        val duration = durationText(item.turn.startedAt, item.turn.completedAt)
        val parts = mutableListOf<String>()
        parts += driverLabel(item.turn.driver)
        if (duration.isNotBlank()) parts += duration
        parts += "$toolCount 个工具"
        parts += workspaceModeLabel(item)
        dirtyPolicyLabel(item.turn.dirtyPolicy).takeIf { it.isNotBlank() }?.let { parts += it }
        if (item.turn.baseCommit.isNotBlank()) {
            parts += "基线 ${item.turn.baseCommit.take(8)}"
        }
        return parts.joinToString(" · ")
    }

    private fun compactMetaLine(item: OpencodeTurnConversationItem): String {
        if (item.turn.driver == OPENCODE_NATIVE_HISTORY_DRIVER) return "只读历史"
        val toolCount = item.timeline.count { it.type == "tool_call" }
        val duration = durationText(item.turn.startedAt, item.turn.completedAt)
        val parts = mutableListOf<String>()
        if (duration.isNotBlank()) parts += duration
        if (toolCount > 0) parts += "$toolCount 个工具"
        if (item.turn.status in setOf("accepted", "running")) {
            parts += workspaceModeLabel(item)
        }
        return parts.joinToString(" · ")
    }

    private fun workspaceModeLabel(item: OpencodeTurnConversationItem): String {
        if (item.turn.driver == OPENCODE_NATIVE_HISTORY_DRIVER) return "只读历史"
        val hasOldWorktree = item.turn.worktreeRoot.isNotBlank() ||
            item.worktree?.exists == true ||
            item.timeline.any { it.rawKind == "worktree.ready" }
        return if (hasOldWorktree) "旧隔离工作区" else "项目工作区"
    }

    private fun dirtyPolicyLabel(policy: String): String {
        return when (policy) {
            "head_only" -> "校验 HEAD"
            "allow_dirty" -> "允许已有改动"
            "" -> ""
            else -> policy
        }
    }

    private fun driverLabel(driver: String): String {
        return when (driver) {
            OPENCODE_NATIVE_HISTORY_DRIVER -> "历史消息"
            OPENCODE_MIRROR_DRIVER -> "Opencode mirror"
            "cli_adapter", "" -> "Opencode CLI"
            else -> driver
        }
    }

    private fun displayError(value: String): String {
        val text = value.trim()
        return if (text.isBlank() || text.equals("null", ignoreCase = true)) "" else text
    }

    private fun processDetails(item: OpencodeTurnConversationItem, full: Boolean): String {
        val events = item.timeline
            .filter { it.type != "assistant_text" }
            .let { if (full) it else it.takeLast(12) }
        if (events.isEmpty()) return ""
        return events.joinToString("\n\n") { event ->
            val title = processEventTitle(event)
            val body = processEventBody(event, full)
            if (body.isBlank()) {
                title
            } else {
                "$title\n$body"
            }
        }
    }

    private fun processEventCount(item: OpencodeTurnConversationItem): Int {
        return item.timeline.count { it.type != "assistant_text" }
    }

    private fun processToggleLine(item: OpencodeTurnConversationItem, detailLevel: Int): String {
        val count = processEventCount(item)
        val latest = item.timeline.lastOrNull { it.type != "assistant_text" }?.let { processEventTitle(it) }.orEmpty()
        val action = when (detailLevel) {
            0 -> "展开摘要"
            1 -> "查看全部"
            else -> "收起"
        }
        return listOf(
            "运行记录",
            "${count}条",
            latest.take(32),
            action
        ).filter { it.isNotBlank() }.joinToString(" · ")
    }

    private fun processEventTitle(event: OpencodeTimelineItem): String {
        val kind = when {
            event.severity == "error" || event.type == "error" -> "错误"
            event.type == "tool_call" -> "工具"
            event.type == "worktree" -> "工作区"
            event.type == "lifecycle" -> "生命周期"
            event.type == "permission" -> "权限"
            event.type == "question" -> "提问"
            event.type == "log" -> "日志"
            else -> event.type.ifBlank { event.rawKind.ifBlank { "事件" } }
        }
        val name = when (event.rawKind) {
            "native_session.bound" -> "opencode 会话已就绪"
            "native_session.resume" -> "续用 opencode 会话"
            "workspace.ready" -> "项目工作区已准备"
            "worktree.ready" -> "旧隔离工作区已准备"
            else -> event.title
        }
            .removePrefix("Tool:")
            .removePrefix("Event:")
            .trim()
        val time = shortTime(event.occurredAt)
        return listOf(time, kind, name).filter { it.isNotBlank() }.joinToString(" · ")
    }

    private fun processEventBody(event: OpencodeTimelineItem, full: Boolean): String {
        val body = if (full && event.detail.isNotBlank()) {
            listOf(event.body, event.detail).filter { it.isNotBlank() }.joinToString("\n")
        } else {
            event.body.ifBlank { event.detail }
        }.trim().replace("\\/", "/")
        if (body.isBlank()) return ""
        val maxLines = if (full) 40 else 4
        val maxChars = if (full) 3000 else 500
        return body
            .lineSequence()
            .map { it.trim() }
            .filter { it.isNotBlank() }
            .take(maxLines)
            .joinToString("\n")
            .take(maxChars)
    }

    private fun questionSummary(question: OpencodeQuestionRequest): String {
        val questions = question.questionsJson ?: JSONArray()
        if (questions.length() == 0) return "Opencode 需要你选择下一步。"
        val lines = mutableListOf<String>()
        for (index in 0 until questions.length().coerceAtMost(3)) {
            val item = questions.optJSONObject(index) ?: continue
            val text = item.optString("question")
                .ifBlank { item.optString("header") }
                .ifBlank { "请选择" }
            val options = item.optJSONArray("options")
            lines += if (options != null && options.length() > 0) {
                "$text · ${options.length()} 个选项"
            } else {
                text
            }
        }
        if (questions.length() > 3) {
            lines += "+${questions.length() - 3} 个问题"
        }
        return lines.joinToString("\n").ifBlank { "Opencode 需要你选择下一步。" }
    }

    private fun quickQuestionOptions(question: OpencodeQuestionRequest): List<QuestionOption> {
        val questions = question.questionsJson ?: return emptyList()
        if (questions.length() != 1) return emptyList()
        val info = questions.optJSONObject(0) ?: return emptyList()
        if (info.optBoolean("multiple", false) || info.optBoolean("custom", false)) return emptyList()
        val options = info.optJSONArray("options") ?: return emptyList()
        val out = mutableListOf<QuestionOption>()
        for (index in 0 until options.length()) {
            val option = options.opt(index)
            val parsed = when (option) {
                is JSONObject -> QuestionOption(
                    label = option.optString("label")
                        .ifBlank { option.optString("name") }
                        .ifBlank { option.optString("value") },
                    value = option.optString("value")
                        .ifBlank { option.optString("id") }
                        .ifBlank { option.optString("label") }
                )
                else -> QuestionOption(label = options.optString(index), value = options.optString(index))
            }
            if (parsed.label.isNotBlank()) out += parsed
        }
        return out
    }

    private fun renderMarkdown(text: String): CharSequence {
        if (text.length > 30000) return text
        markdownCache[text]?.let { return it }
        val rendered = buildMarkdownText(text)
        markdownCache[text] = rendered
        return rendered
    }

    private fun buildMarkdownText(text: String): CharSequence {
        val builder = SpannableStringBuilder()
        var inCodeBlock = false
        val lines = text.replace("\r\n", "\n").split("\n")
        for ((index, rawLine) in lines.withIndex()) {
            val line = rawLine.trimEnd()
            if (line.trimStart().startsWith("```")) {
                inCodeBlock = !inCodeBlock
                continue
            }
            if (builder.isNotEmpty()) builder.append('\n')
            val start = builder.length
            if (inCodeBlock) {
                builder.append(line)
                builder.setSpan(TypefaceSpan("monospace"), start, builder.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                builder.setSpan(BackgroundColorSpan(Color.parseColor("#F1F5F9")), start, builder.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                builder.setSpan(ForegroundColorSpan(Color.parseColor("#0F172A")), start, builder.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                continue
            }
            appendMarkdownLine(builder, line)
            if (index == lines.lastIndex && builder.length > start && builder[builder.length - 1] == '\n') {
                builder.delete(builder.length - 1, builder.length)
            }
        }
        return builder
    }

    private fun appendMarkdownLine(builder: SpannableStringBuilder, rawLine: String) {
        val trimmed = rawLine.trimStart()
        val indent = rawLine.length - trimmed.length
        val start = builder.length
        when {
            trimmed.startsWith("#") && trimmed.dropWhile { it == '#' }.startsWith(" ") -> {
                val level = trimmed.takeWhile { it == '#' }.length.coerceIn(1, 4)
                val text = trimmed.drop(level).trimStart()
                appendInlineMarkdown(builder, text)
                builder.setSpan(StyleSpan(Typeface.BOLD), start, builder.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                builder.setSpan(RelativeSizeSpan(if (level == 1) 1.18f else 1.08f), start, builder.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
            }
            trimmed.startsWith(">") -> {
                builder.append("│ ")
                appendInlineMarkdown(builder, trimmed.removePrefix(">").trimStart())
                builder.setSpan(ForegroundColorSpan(Color.parseColor("#475569")), start, builder.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
            }
            trimmed.startsWith("- ") || trimmed.startsWith("* ") -> {
                repeat(indent / 2) { builder.append("  ") }
                builder.append("• ")
                appendInlineMarkdown(builder, trimmed.drop(2))
            }
            Regex("^\\d+[.)]\\s+.*").matches(trimmed) -> {
                repeat(indent / 2) { builder.append("  ") }
                appendInlineMarkdown(builder, trimmed)
            }
            else -> appendInlineMarkdown(builder, rawLine)
        }
    }

    private fun appendInlineMarkdown(builder: SpannableStringBuilder, text: String) {
        var index = 0
        while (index < text.length) {
            when {
                text.startsWith("**", index) -> {
                    val end = text.indexOf("**", startIndex = index + 2)
                    if (end > index + 2) {
                        val start = builder.length
                        builder.append(text.substring(index + 2, end))
                        builder.setSpan(StyleSpan(Typeface.BOLD), start, builder.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                        index = end + 2
                    } else {
                        builder.append(text[index])
                        index++
                    }
                }
                text[index] == '`' -> {
                    val end = text.indexOf('`', startIndex = index + 1)
                    if (end > index + 1) {
                        val start = builder.length
                        builder.append(text.substring(index + 1, end))
                        builder.setSpan(TypefaceSpan("monospace"), start, builder.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                        builder.setSpan(BackgroundColorSpan(Color.parseColor("#E2E8F0")), start, builder.length, Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
                        index = end + 1
                    } else {
                        builder.append(text[index])
                        index++
                    }
                }
                else -> {
                    builder.append(text[index])
                    index++
                }
            }
        }
    }

    private fun permissionSummary(permission: OpencodePermissionRequest): String {
        val resource = permission.resourceJson
        val readable = if (resource == null) {
            ""
        } else {
            readableResource(resource)
        }
        return buildString {
            append("需要授权")
            append("\n")
            append(permission.kind.ifBlank { "permission" })
            if (readable.isNotBlank()) {
                append("\n")
                append(readable)
            }
        }
    }

    private fun readableResource(resource: JSONObject): String {
        val tool = resource.optString("tool").ifBlank { resource.optString("name") }
        val command = resource.optString("command").ifBlank { resource.optString("cmd") }
        val path = resource.optString("path").ifBlank { resource.optString("file") }
        val target = listOf(tool, command, path).firstOrNull { it.isNotBlank() }
        if (!target.isNullOrBlank()) {
            return target.take(500)
        }
        return resource.toString(2).take(600)
    }

    private fun compactTime(value: String): String {
        return value.replace("T", " ").removeSuffix("Z").take(16)
    }

    private fun shortTime(value: String): String {
        return value.replace("T", " ").removeSuffix("Z").drop(11).take(5)
    }

    private fun relativeTime(value: String): String {
        return runCatching {
            val seconds = Duration.between(Instant.parse(value), Instant.now()).seconds.coerceAtLeast(0)
            when {
                seconds < 10 -> "刚刚"
                seconds < 60 -> "${seconds}s 前"
                seconds < 3600 -> "${seconds / 60}m 前"
                else -> compactTime(value)
            }
        }.getOrDefault(compactTime(value))
    }

    private fun durationText(startedAt: String, completedAt: String): String {
        if (startedAt.isBlank() || completedAt.isBlank()) return ""
        return runCatching {
            val duration = Duration.between(Instant.parse(startedAt), Instant.parse(completedAt))
            val seconds = duration.seconds.coerceAtLeast(0)
            when {
                seconds < 60 -> "${seconds}s"
                seconds < 3600 -> "${seconds / 60}m${seconds % 60}s"
                else -> "${seconds / 3600}h${(seconds % 3600) / 60}m"
            }
        }.getOrDefault("")
    }

    private fun dp(value: Int): Int {
        return (value * Resources.getSystem().displayMetrics.density).toInt()
    }

    private data class QuestionOption(
        val label: String,
        val value: String
    )
}
