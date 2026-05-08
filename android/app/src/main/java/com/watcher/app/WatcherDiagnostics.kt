package com.watcher.app

import android.content.Context
import android.os.Build
import java.io.PrintWriter
import java.io.StringWriter
import java.time.Instant
import kotlin.system.exitProcess

object WatcherDiagnostics {
    private const val prefsName = "watcher_diagnostics"
    private const val lastCrashAtKey = "last_crash_at"
    private const val lastCrashTextKey = "last_crash_text"

    fun install(context: Context) {
        val appContext = context.applicationContext
        val previousHandler = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, throwable ->
            saveCrash(appContext, thread, throwable)
            if (previousHandler != null) {
                previousHandler.uncaughtException(thread, throwable)
            } else {
                exitProcess(2)
            }
        }
    }

    fun lastCrash(context: Context): String {
        val prefs = context.getSharedPreferences(prefsName, Context.MODE_PRIVATE)
        val occurredAt = prefs.getString(lastCrashAtKey, "").orEmpty()
        val crash = prefs.getString(lastCrashTextKey, "").orEmpty()
        if (crash.isBlank()) {
            return "No recorded app crash."
        }
        return buildString {
            append("Last crash")
            if (occurredAt.isNotBlank()) {
                append(" · ")
                append(occurredAt)
            }
            append("\n")
            append(crash)
        }
    }

    fun buildDebugReport(context: Context, api: WatcherApi): String {
        val runtime = Runtime.getRuntime()
        val config = api.currentConfig()
        val registration = api.currentRegistration()
        return buildString {
            append("Watcher Android Debug Report\n")
            append("generated_at=").append(Instant.now().toString()).append("\n")
            append("app=").append(api.currentAppVersionName()).append(" (").append(api.currentAppVersionCode()).append(")\n")
            if (BuildConfig.BUILD_WATERMARK.isNotBlank()) {
                append("build_watermark=").append(BuildConfig.BUILD_WATERMARK).append("\n")
            }
            append("package=").append(context.packageName).append("\n")
            append("device=").append(Build.MANUFACTURER).append(" ").append(Build.MODEL).append("\n")
            append("android_sdk=").append(Build.VERSION.SDK_INT).append(" release=").append(Build.VERSION.RELEASE).append("\n")
            append("abi=").append(Build.SUPPORTED_ABIS.joinToString(",")).append("\n")
            append("relay_url=").append(config.baseUrl).append("\n")
            append("owner_token_configured=").append(config.ownerToken.isNotBlank()).append("\n")
            append("device_id=").append(registration?.deviceId.orEmpty().ifBlank { "not_registered" }).append("\n")
            append("memory_used_mb=").append((runtime.totalMemory() - runtime.freeMemory()) / 1024 / 1024).append("\n")
            append("memory_total_mb=").append(runtime.totalMemory() / 1024 / 1024).append("\n")
            append("memory_max_mb=").append(runtime.maxMemory() / 1024 / 1024).append("\n\n")
            append(lastCrash(context))
        }
    }

    private fun saveCrash(context: Context, thread: Thread, throwable: Throwable) {
        val writer = StringWriter()
        PrintWriter(writer).use { printWriter ->
            printWriter.println("${throwable::class.java.name}: ${throwable.message.orEmpty()}")
            printWriter.println("thread=${thread.name}")
            throwable.printStackTrace(printWriter)
        }
        context.getSharedPreferences(prefsName, Context.MODE_PRIVATE)
            .edit()
            .putString(lastCrashAtKey, Instant.now().toString())
            .putString(lastCrashTextKey, writer.toString().take(12000))
            .apply()
    }
}
