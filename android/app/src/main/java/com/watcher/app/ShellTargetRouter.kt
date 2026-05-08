package com.watcher.app

import android.content.Context
import android.content.Intent

object ShellTargetRouter {
    fun intentFor(context: Context, target: ShellTarget, fallbackTitle: String = ""): Intent {
        val intent = when (target.componentId) {
            "codex" -> codexIntent(context, target)
            "box" -> boxIntent(context, target)
            "pilot" -> pilotIntent(context, target, fallbackTitle)
            "cc" -> ccIntent(context, target, fallbackTitle)
            "opencode" -> opencodeIntent(context, target, fallbackTitle)
            "host" -> Intent(context, HostActivity::class.java)
            "game" -> Intent(context, BlockGameActivity::class.java)
            else -> fallbackIntent(context, target)
        }
        return intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
    }

    private fun codexIntent(context: Context, target: ShellTarget): Intent {
        if (target.surface == "thread" && target.resourceId.isNotBlank()) {
            return Intent(context, CodexThreadActivity::class.java)
                .putExtra("thread_id", target.resourceId)
        }
        return Intent(context, CodexSessionsActivity::class.java)
    }

    private fun boxIntent(context: Context, target: ShellTarget): Intent {
        if (target.surface == "feed" || target.surface == "event") {
            return Intent(context, BoxFeedActivity::class.java)
        }
        return Intent(context, BoxActivity::class.java)
    }

    private fun pilotIntent(context: Context, target: ShellTarget, fallbackTitle: String): Intent {
        if (target.resourceId.isNotBlank()) {
            return Intent(context, PilotChatActivity::class.java)
                .putExtra("session_id", target.resourceId)
                .putExtra("session_title", fallbackTitle.ifBlank { "Pilot" })
        }
        return Intent(context, MainActivity::class.java)
    }

    private fun ccIntent(context: Context, target: ShellTarget, fallbackTitle: String): Intent {
        if (target.surface == "session" && target.resourceId.isNotBlank()) {
            return Intent(context, CcMimoSessionActivity::class.java)
                .putExtra("session_id", target.resourceId)
                .putExtra("session_title", fallbackTitle.ifBlank { "CC" })
        }
        return Intent(context, CcMimoSessionsActivity::class.java)
    }

    private fun opencodeIntent(context: Context, target: ShellTarget, fallbackTitle: String): Intent {
        if (target.surface == "session" && target.resourceId.isNotBlank()) {
            val intent = Intent(context, OpencodeSessionActivity::class.java)
                .putExtra("session_id", target.resourceId)
                .putExtra("session_title", fallbackTitle.ifBlank { "Opencode" })
            if (target.resourceId.startsWith("ses_")) {
                intent.putExtra("native_session_id", target.resourceId)
            }
            return intent
        }
        return Intent(context, OpencodeSessionsActivity::class.java)
    }

    private fun fallbackIntent(context: Context, target: ShellTarget): Intent {
        if (target.surface == "settings") {
            return Intent(context, SettingsActivity::class.java)
        }
        if (target.componentId.isNotBlank()) {
            return Intent(context, ModuleDetailActivity::class.java)
                .putExtra("component_id", target.componentId)
                .putExtra("surface", target.surface)
                .putExtra("resource_id", target.resourceId)
        }
        return Intent(context, MainActivity::class.java)
    }
}
