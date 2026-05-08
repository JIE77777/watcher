package com.watcher.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

data class BoxSourceEntry(
    val adapter: BoxAdapterInfo,
    val catalog: BoxCatalog?,
    val error: String = ""
)

class BoxSourceAdapter(
    private val onClick: (BoxSourceEntry) -> Unit
) : RecyclerView.Adapter<BoxSourceAdapter.VH>() {
    private val entries = mutableListOf<BoxSourceEntry>()

    fun submit(list: List<BoxSourceEntry>) {
        entries.clear()
        entries.addAll(list)
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): VH {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_box_source, parent, false)
        return VH(view, onClick)
    }

    override fun onBindViewHolder(holder: VH, position: Int) {
        holder.bind(entries[position])
    }

    override fun getItemCount(): Int = entries.size

    class VH(
        itemView: View,
        private val onClick: (BoxSourceEntry) -> Unit
    ) : RecyclerView.ViewHolder(itemView) {
        private val titleText: TextView = itemView.findViewById(R.id.boxSourceTitle)
        private val summaryText: TextView = itemView.findViewById(R.id.boxSourceSummary)
        private val metaText: TextView = itemView.findViewById(R.id.boxSourceMeta)
        private val stateText: TextView = itemView.findViewById(R.id.boxSourceState)
        private var current: BoxSourceEntry? = null

        init {
            itemView.setOnClickListener {
                val entry = current ?: return@setOnClickListener
                if (entry.catalog != null && entry.error.isBlank()) {
                    onClick(entry)
                }
            }
        }

        fun bind(entry: BoxSourceEntry) {
            current = entry
            val context = itemView.context
            val catalog = entry.catalog
            val title = catalog?.title?.ifBlank { entry.adapter.title } ?: entry.adapter.title
            titleText.text = title.ifBlank { entry.adapter.id }
            summaryText.text = when {
                entry.error.isNotBlank() -> entry.error
                catalog?.description?.isNotBlank() == true -> catalog.description
                entry.adapter.description.isNotBlank() -> entry.adapter.description
                else -> context.watcherText("可配置 Box 信息源", "Configurable box source")
            }
            metaText.text = if (catalog != null) {
                context.watcherText(
                    "${catalog.views.size} 个视图 · ${catalog.datasets.size} 个数据集",
                    "${catalog.views.size} views · ${catalog.datasets.size} datasets"
                )
            } else {
                context.watcherText("不可用", "Unavailable")
            }
            val enabled = catalog != null && entry.error.isBlank()
            itemView.isEnabled = enabled
            itemView.alpha = if (enabled) 1f else 0.62f
            stateText.text = if (enabled) ">" else "!"
        }
    }
}
