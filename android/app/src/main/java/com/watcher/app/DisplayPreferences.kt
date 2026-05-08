package com.watcher.app

import android.content.Context
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

private const val displayPrefsName = "watcher_prefs"
private const val languageKey = "display_language"
private const val timeZoneKey = "display_time_zone"
private const val defaultLanguage = "zh"
private const val defaultTimeZone = "Asia/Shanghai"

fun Context.currentDisplayConfig(): WatcherDisplayConfig {
    val prefs = getSharedPreferences(displayPrefsName, Context.MODE_PRIVATE)
    val language = prefs.getString(languageKey, defaultLanguage).orEmpty()
        .ifBlank { defaultLanguage }
        .lowercase(Locale.US)
    val timeZone = prefs.getString(timeZoneKey, defaultTimeZone).orEmpty()
        .ifBlank { defaultTimeZone }
    return WatcherDisplayConfig(
        language = if (language == "en") "en" else "zh",
        timeZone = safeZoneId(timeZone).id
    )
}

fun Context.saveDisplayConfig(language: String, timeZone: String) {
    val normalizedLanguage = if (language.lowercase(Locale.US) == "en") "en" else "zh"
    val normalizedTimeZone = safeZoneId(timeZone.ifBlank { defaultTimeZone }).id
    getSharedPreferences(displayPrefsName, Context.MODE_PRIVATE)
        .edit()
        .putString(languageKey, normalizedLanguage)
        .putString(timeZoneKey, normalizedTimeZone)
        .apply()
}

fun Context.watcherText(zh: String, en: String): String {
    return if (currentDisplayConfig().language == "en") en else zh
}

fun Context.displayTime(raw: String, fallback: String = raw): String {
    val value = raw.trim()
    if (value.isBlank()) {
        return ""
    }
    val instant = parseInstant(value) ?: return fallback
    val config = currentDisplayConfig()
    val zoned = instant.atZone(safeZoneId(config.timeZone))
    val formatter = if (config.language == "en") {
        if (zoned.year == Instant.now().atZone(zoned.zone).year) {
            DateTimeFormatter.ofPattern("MMM d HH:mm", Locale.US)
        } else {
            DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm", Locale.US)
        }
    } else {
        if (zoned.year == Instant.now().atZone(zoned.zone).year) {
            DateTimeFormatter.ofPattern("MM-dd HH:mm", Locale.US)
        } else {
            DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm", Locale.US)
        }
    }
    return formatter.format(zoned)
}

fun Context.displayTimeZoneLabel(): String {
    return currentDisplayConfig().timeZone
}

private fun parseInstant(value: String): Instant? {
    return runCatching { Instant.parse(value) }.getOrNull()
        ?: runCatching { OffsetDateTime.parse(value).toInstant() }.getOrNull()
}

private fun safeZoneId(value: String): ZoneId {
    return runCatching { ZoneId.of(value.trim()) }.getOrDefault(ZoneId.of(defaultTimeZone))
}
