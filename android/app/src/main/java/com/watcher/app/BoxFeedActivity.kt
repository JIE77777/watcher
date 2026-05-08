package com.watcher.app

import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

class BoxFeedActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var notifications: NotificationHelper
    private lateinit var statusText: TextView
    private lateinit var emptyStateText: TextView
    private lateinit var adapter: TaskFeedAdapter

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_box_feed)
        installSystemBarInsets(findViewById(R.id.boxFeedRoot))

        api = WatcherApi(this)
        notifications = NotificationHelper(this)
        statusText = findViewById(R.id.boxFeedStatusText)
        emptyStateText = findViewById(R.id.boxFeedEmptyStateText)
        val refreshButton: Button = findViewById(R.id.boxFeedRefreshButton)
        val recyclerView: RecyclerView = findViewById(R.id.boxFeedRecycler)

        adapter = TaskFeedAdapter { event -> openEventDetail(event) }
        recyclerView.layoutManager = LinearLayoutManager(this)
        recyclerView.adapter = adapter

        refreshButton.setOnClickListener { refreshFeed() }

        val sourcesButton: Button = findViewById(R.id.boxFeedSourcesButton)
        sourcesButton.text = watcherText("信息源", "Sources")
        sourcesButton.setOnClickListener {
            startActivity(Intent(this, BoxActivity::class.java))
        }
    }

    override fun onResume() {
        super.onResume()
        renderCachedFeed()
        refreshFeed()
    }

    private fun renderCachedFeed() {
        val cached = api.loadCachedWatcherTaskEvents().sortedByDescending { it.occurredAt }
        adapter.submitList(cached)
        emptyStateText.visibility = if (cached.isEmpty()) View.VISIBLE else View.GONE
    }

    private fun refreshFeed() {
        statusText.text = watcherText("同步 watcher.task…", "Syncing watcher.task…")
        Thread {
            try {
                val sync = api.syncWatcherTaskFeed(forceRegister = false)
                runOnUiThread {
                    adapter.submitList(sync.events)
                    emptyStateText.visibility = if (sync.events.isEmpty()) View.VISIBLE else View.GONE
                    if (sync.notificationsEligible && sync.newlyAddedEvents.isNotEmpty()) {
                        notifications.showNewEvents(sync.newlyAddedEvents)
                    }
                    statusText.text = if (sync.newEvents > 0) {
                        watcherText("${sync.events.size} 条 · 新 ${sync.newEvents}", "${sync.events.size} items · ${sync.newEvents} new")
                    } else {
                        watcherText("${sync.events.size} 条", "${sync.events.size} items")
                    }
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = watcherText("同步失败：${exc.message}", "Sync failed: ${exc.message}")
                    renderCachedFeed()
                }
            }
        }.start()
    }

    private fun openEventDetail(event: WatcherTaskEvent) {
        startActivity(
            Intent(this, EventDetailActivity::class.java)
                .putExtra("title", event.displayTitle)
                .putExtra("summary", event.summary)
                .putExtra("body", event.body)
                .putExtra("task_id", event.taskId)
                .putExtra("resource_id", event.resourceId)
                .putExtra("change_type", event.changeType)
                .putExtra("occurred_at", event.occurredAt)
                .putStringArrayListExtra("labels", ArrayList(event.labels))
        )
    }
}
