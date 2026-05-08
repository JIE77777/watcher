package com.watcher.app

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView

class HistoryBestAdapter : RecyclerView.Adapter<HistoryBestAdapter.VH>() {
    private var entries: List<BoxHistoryBestEntry> = emptyList()

    fun submitList(list: List<BoxHistoryBestEntry>) {
        entries = list
        notifyDataSetChanged()
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): VH {
        val view = LayoutInflater.from(parent.context)
            .inflate(R.layout.item_leaderboard_entry, parent, false)
        return VH(view)
    }

    override fun onBindViewHolder(holder: VH, position: Int) {
        holder.bind(entries[position])
    }

    override fun getItemCount() = entries.size

    class VH(itemView: View) : RecyclerView.ViewHolder(itemView) {
        private val rankText: TextView = itemView.findViewById(R.id.entryRank)
        private val teamText: TextView = itemView.findViewById(R.id.entryTeam)
        private val scoreText: TextView = itemView.findViewById(R.id.entryScore)
        private val subsText: TextView = itemView.findViewById(R.id.entrySubs)

        fun bind(entry: BoxHistoryBestEntry) {
            rankText.text = "#${entry.bestRank}"
            teamText.text = if (entry.unit.isBlank()) entry.team else "${entry.team} · ${entry.unit}"
            scoreText.text = formatScore(entry.bestScore)
            subsText.text = "x${entry.subs}"
        }

        private fun formatScore(score: Double?): String {
            if (score == null) return "-"
            return if (score == score.toLong().toDouble()) {
                score.toLong().toString()
            } else {
                String.format("%.1f", score)
            }
        }
    }
}
