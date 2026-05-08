package com.watcher.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

class BoxRecordAdapter : RecyclerView.Adapter<BoxRecordAdapter.VH>() {
    private var columns: List<BoxViewColumn> = emptyList()
    private var records: List<BoxDatasetRecord> = emptyList()

    fun submit(viewColumns: List<BoxViewColumn>, list: List<BoxDatasetRecord>) {
        columns = viewColumns
        records = list
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): VH {
        val view = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_box_record, parent, false)
        return VH(view)
    }

    override fun onBindViewHolder(holder: VH, position: Int) {
        holder.bind(columns, records[position], position)
    }

    override fun getItemCount() = records.size

    class VH(itemView: View) : RecyclerView.ViewHolder(itemView) {
        private val leadText: TextView = itemView.findViewById(R.id.boxRecordLead)
        private val titleText: TextView = itemView.findViewById(R.id.boxRecordTitle)
        private val valueText: TextView = itemView.findViewById(R.id.boxRecordValue)
        private val metaText: TextView = itemView.findViewById(R.id.boxRecordMeta)

        fun bind(columns: List<BoxViewColumn>, record: BoxDatasetRecord, position: Int) {
            val data = record.data
            leadText.text = leadValue(data, position)
            titleText.text = buildTitle(record, data)
            valueText.text = trailingValue(columns, data)
            metaText.text = formatMeta(record, data)
        }

        private fun buildTitle(record: BoxDatasetRecord, data: Map<String, Any?>): String {
            return valueText(record.title)
                .ifBlank { valueText(data["title"] ?: data["model"] ?: data["team"] ?: data["name"] ?: record.id) }
        }

        private fun formatMeta(record: BoxDatasetRecord, data: Map<String, Any?>): String {
            val subtitle = valueText(record.subtitle).ifBlank { valueText(data["subtitle"]) }
            val provider = valueText(data["provider"])
            val unit = valueText(data["unit"])
            val topic = valueText(data["topic"] ?: data["category"])
            val last = valueText(data["last_submit"] ?: data["last_commit"])
            val updated = valueText(data["updated_at"])
            val subs = valueText(data["subs"] ?: data["commit_times"])
            return listOf(
                subtitle,
                provider,
                unit,
                topic,
                last,
                updated,
                if (subs.isNotBlank()) "x$subs" else ""
            ).filter { it.isNotBlank() }.joinToString(" · ")
        }

        private fun leadValue(data: Map<String, Any?>, position: Int): String {
            return valueText(data["rank"] ?: data["best_rank"] ?: data["index"])
                .ifBlank { (position + 1).toString() }
        }

        private fun trailingValue(columns: List<BoxViewColumn>, data: Map<String, Any?>): String {
            val preferredField = columns.firstOrNull { column ->
                val field = column.field
                field == "score" ||
                    field == "best_score" ||
                    field == "current_score" ||
                    field == "take_time" ||
                    column.type == "score" ||
                    column.type == "number"
            }?.field
            val value = if (preferredField != null) data[preferredField] else firstExisting(
                data,
                listOf("score", "best_score", "current_score", "take_time")
            )
            return formatValue(value)
        }

        private fun firstExisting(data: Map<String, Any?>, fields: List<String>): Any? {
            for (field in fields) {
                if (data.containsKey(field)) return data[field]
            }
            return null
        }

        private fun formatValue(value: Any?): String {
            if (value == null) return "-"
            val n = value as? Number
            if (n != null) {
                val d = n.toDouble()
                return if (d == d.toLong().toDouble()) d.toLong().toString() else String.format("%.1f", d)
            }
            return value.toString()
        }

        private fun valueText(value: Any?): String {
            return value?.toString()?.takeIf { it != "null" } ?: ""
        }
    }
}
