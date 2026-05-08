package com.watcher.app

import android.os.Bundle
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity

class EventDetailActivity : AppCompatActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_event_detail)
        installSystemBarInsets(findViewById(R.id.detailRoot))

        val titleView: TextView = findViewById(R.id.detailTitle)
        val metaView: TextView = findViewById(R.id.detailMeta)
        val bodyView: TextView = findViewById(R.id.detailBody)

        val labels = intent.getStringArrayListExtra("labels").orEmpty()
        val occurredAt = intent.getStringExtra("occurred_at").orEmpty()
        val displayOccurredAt = displayTime(occurredAt).ifBlank { occurredAt }
        val taskId = intent.getStringExtra("task_id").orEmpty()
        val resourceId = intent.getStringExtra("resource_id").orEmpty()
        val changeType = intent.getStringExtra("change_type").orEmpty()

        titleView.text = intent.getStringExtra("title").orEmpty()
        metaView.text = buildString {
            append(displayOccurredAt)
            if (changeType.isNotBlank()) {
                append("\n")
                append("change: ")
                append(changeType)
            }
            if (taskId.isNotBlank()) {
                append("\n")
                append("task: ")
                append(taskId)
            }
            if (resourceId.isNotBlank()) {
                append("\n")
                append("resource: ")
                append(resourceId)
            }
            if (labels.isNotEmpty()) {
                append("\n")
                append(labels.joinToString(" | "))
            }
        }
        bodyView.text = intent.getStringExtra("body").orEmpty()
    }
}
