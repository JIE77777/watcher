package com.watcher.app

import android.content.Context
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.widget.LinearLayout
import android.widget.TextView
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

class BoxPagerAdapter(
    private val context: Context
) : RecyclerView.Adapter<BoxPagerAdapter.PageVH>() {

    private var pages: List<BoxDatasetResult> = emptyList()

    fun setPages(pageList: List<BoxDatasetResult>) {
        pages = pageList
        notifyDataSetChanged()
    }

    override fun getItemCount() = pages.size

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): PageVH {
        val scrollContent = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(32, 16, 32, 0)
        }

        val scrollView = android.widget.ScrollView(context).apply {
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
            addView(scrollContent)
        }

        return PageVH(scrollView, scrollContent)
    }

    override fun onBindViewHolder(holder: PageVH, position: Int) {
        val page = pages[position]
        holder.content.removeAllViews()
        val groupBy = page.view?.groupBy.orEmpty()
        val sections = if (groupBy.isBlank()) {
            listOf("" to page.records)
        } else {
            page.records
                .groupBy { it.data[groupBy]?.toString().orEmpty().ifBlank { context.watcherText("其他", "Other") } }
                .toList()
        }

        if (sections.all { it.second.isEmpty() }) {
            holder.content.addView(makeEmptyText(context.watcherText("暂无数据", "No data")).apply {
                visibility = View.VISIBLE
            })
            return
        }

        for ((labelText, records) in sections) {
            if (records.isEmpty()) continue
            if (labelText.isNotBlank()) {
                holder.content.addView(makeSectionLabel(labelText))
            }
            val adapter = BoxRecordAdapter()
            adapter.submit(page.view?.columns ?: emptyList(), records)
            holder.content.addView(makeRecycler().apply { this.adapter = adapter })
        }
    }

    private fun makeSectionLabel(text: String): TextView {
        return TextView(context).apply {
            this.text = text
            setTextColor(0xFF0F172A.toInt())
            textSize = 16f
            setTypeface(null, android.graphics.Typeface.BOLD)
            setPadding(0, 24, 0, 8)
        }
    }

    private fun makeEmptyText(text: String): TextView {
        return TextView(context).apply {
            this.text = text
            setTextColor(0xFF64748B.toInt())
            textSize = 14f
            gravity = Gravity.CENTER_HORIZONTAL
            setPadding(0, 16, 0, 16)
            visibility = View.GONE
        }
    }

    private fun makeRecycler(): RecyclerView {
        return RecyclerView(context).apply {
            layoutParams = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.WRAP_CONTENT
            ).apply { setMargins(0, 0, 0, 16) }
            layoutManager = LinearLayoutManager(context)
            isNestedScrollingEnabled = false
        }
    }

    class PageVH(
        itemView: View,
        val content: LinearLayout
    ) : RecyclerView.ViewHolder(itemView)
}
