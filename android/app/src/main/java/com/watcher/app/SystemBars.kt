package com.watcher.app

import android.view.View
import androidx.activity.enableEdgeToEdge
import androidx.appcompat.app.AppCompatActivity
import androidx.core.graphics.Insets
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat

fun AppCompatActivity.installSystemBarInsets(root: View, bottomInsetView: View? = null) {
    enableEdgeToEdge()

    val initialPadding = Insets.of(
        root.paddingLeft,
        root.paddingTop,
        root.paddingRight,
        root.paddingBottom
    )
    val initialBottomInsetPadding = bottomInsetView?.let {
        Insets.of(
            it.paddingLeft,
            it.paddingTop,
            it.paddingRight,
            it.paddingBottom
        )
    }

    ViewCompat.setOnApplyWindowInsetsListener(root) { view, windowInsets ->
        val systemInsets = windowInsets.getInsets(
            WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout()
        )
        view.setPadding(
            initialPadding.left + systemInsets.left,
            initialPadding.top + systemInsets.top,
            initialPadding.right + systemInsets.right,
            if (bottomInsetView == null) {
                initialPadding.bottom + systemInsets.bottom
            } else {
                initialPadding.bottom
            }
        )
        if (bottomInsetView != null && initialBottomInsetPadding != null) {
            val imeInsets = windowInsets.getInsets(WindowInsetsCompat.Type.ime())
            val bottomPadding = maxOf(systemInsets.bottom, imeInsets.bottom)
            bottomInsetView.setPadding(
                initialBottomInsetPadding.left,
                initialBottomInsetPadding.top,
                initialBottomInsetPadding.right,
                initialBottomInsetPadding.bottom + bottomPadding
            )
        }
        windowInsets
    }
    ViewCompat.requestApplyInsets(root)
}
