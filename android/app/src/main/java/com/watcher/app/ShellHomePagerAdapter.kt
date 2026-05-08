package com.watcher.app

import android.content.Context
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView
import androidx.recyclerview.widget.GridLayoutManager
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

class ShellHomePagerAdapter(
    private val onSignalClick: (ShellSignal) -> Unit,
    private val onCellClick: (ComponentCell) -> Unit
) : RecyclerView.Adapter<RecyclerView.ViewHolder>() {
    private var home: ShellHome = ShellHome(
        status = "ready",
        updatedAt = "",
        signals = emptyList(),
        components = emptyList()
    )
    private var tools: List<ComponentCell> = emptyList()

    fun submitHome(value: ShellHome, toolCells: List<ComponentCell>? = null) {
        home = value
        tools = toolCells ?: value.components
        notifyItemRangeChanged(0, itemCount)
    }

    override fun getItemCount(): Int = 2

    override fun getItemViewType(position: Int): Int = position

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): RecyclerView.ViewHolder {
        return if (viewType == 0) {
            SignalsPageHolder(parent, onSignalClick)
        } else {
            ToolsPageHolder(parent, onCellClick)
        }
    }

    override fun onBindViewHolder(holder: RecyclerView.ViewHolder, position: Int) {
        when (holder) {
            is SignalsPageHolder -> holder.bind(home.signals)
            is ToolsPageHolder -> holder.bind(tools)
        }
    }

    private class SignalsPageHolder(
        parent: ViewGroup,
        onSignalClick: (ShellSignal) -> Unit
    ) : RecyclerView.ViewHolder(pageRoot(parent)) {
        private val titleView: TextView
        private val subtitleView: TextView
        private val emptyView: TextView
        private val adapter = ShellSignalsAdapter(onSignalClick)

        init {
            val context = itemView.context
            val container = itemView as LinearLayout
            titleView = pageTitle(context, context.watcherText("Attention", "Attention"))
            subtitleView = pageSubtitle(context, context.watcherText("需要处理的提醒和近期变化。", "Actionable reminders and recent changes."))
            val recyclerView = RecyclerView(context).apply {
                layoutManager = LinearLayoutManager(context)
                adapter = this@SignalsPageHolder.adapter
                clipToPadding = false
                setPadding(0, dp(context, 10), 0, dp(context, 16))
            }
            emptyView = pageEmpty(context, context.watcherText("没有需要处理的事项", "Nothing needs attention"))
            container.addView(titleView)
            container.addView(subtitleView)
            container.addView(emptyView)
            container.addView(recyclerView, LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                0,
                1f
            ))
        }

        fun bind(signals: List<ShellSignal>) {
            val context = itemView.context
            titleView.text = context.watcherText("Attention", "Attention")
            subtitleView.text = context.watcherText("需要处理的提醒和近期变化。", "Actionable reminders and recent changes.")
            emptyView.text = context.watcherText("没有需要处理的事项", "Nothing needs attention")
            emptyView.visibility = if (signals.isEmpty()) View.VISIBLE else View.GONE
            adapter.submitList(signals)
        }
    }

    private class ToolsPageHolder(
        parent: ViewGroup,
        onCellClick: (ComponentCell) -> Unit
    ) : RecyclerView.ViewHolder(pageRoot(parent)) {
        private val titleView: TextView
        private val subtitleView: TextView
        private val emptyView: TextView
        private val adapter = ComponentCellsAdapter(onCellClick)

        init {
            val context = itemView.context
            val container = itemView as LinearLayout
            titleView = pageTitle(context, context.watcherText("Modules", "Modules"))
            subtitleView = pageSubtitle(context, context.watcherText("常用入口和运行状态。", "Common entries and runtime state."))
            val recyclerView = RecyclerView(context).apply {
                layoutManager = GridLayoutManager(context, 2)
                adapter = this@ToolsPageHolder.adapter
                clipToPadding = false
                setPadding(0, dp(context, 10), 0, dp(context, 16))
            }
            emptyView = pageEmpty(context, context.watcherText("No tools", "No tools"))
            container.addView(titleView)
            container.addView(subtitleView)
            container.addView(emptyView)
            container.addView(recyclerView, LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                0,
                1f
            ))
        }

        fun bind(components: List<ComponentCell>) {
            val context = itemView.context
            titleView.text = context.watcherText("Modules", "Modules")
            subtitleView.text = context.watcherText("常用入口和运行状态。", "Common entries and runtime state.")
            emptyView.text = context.watcherText("No modules", "No modules")
            emptyView.visibility = if (components.isEmpty()) View.VISIBLE else View.GONE
            adapter.submitList(components)
        }
    }
}

private fun pageRoot(parent: ViewGroup): LinearLayout {
    val context = parent.context
    return LinearLayout(context).apply {
        layoutParams = RecyclerView.LayoutParams(
            RecyclerView.LayoutParams.MATCH_PARENT,
            RecyclerView.LayoutParams.MATCH_PARENT
        )
        orientation = LinearLayout.VERTICAL
    }
}

private fun pageTitle(context: Context, textValue: String): TextView {
    return TextView(context).apply {
        text = textValue
        setTextColor(0xFF132238.toInt())
        textSize = 25f
        setTypeface(typeface, android.graphics.Typeface.BOLD)
    }
}

private fun pageSubtitle(context: Context, textValue: String): TextView {
    return TextView(context).apply {
        text = textValue
        setTextColor(0xFF64748B.toInt())
        textSize = 13f
        setPadding(0, dp(context, 4), 0, dp(context, 8))
    }
}

private fun pageEmpty(context: Context, textValue: String): TextView {
    return TextView(context).apply {
        text = textValue
        setTextColor(0xFF94A3B8.toInt())
        textSize = 16f
        gravity = android.view.Gravity.CENTER_HORIZONTAL
        setPadding(0, dp(context, 44), 0, dp(context, 10))
    }
}

private fun dp(context: Context, value: Int): Int {
    return (context.resources.displayMetrics.density * value).toInt()
}
