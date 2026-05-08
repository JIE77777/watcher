package com.watcher.app

import android.content.Context
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.RectF
import android.util.AttributeSet
import android.view.MotionEvent
import android.view.View
import kotlin.math.max

class CodexFastScrollerView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null
) : View(context, attrs) {
    var onSeekChanged: ((Float) -> Unit)? = null
    var onDragStateChanged: ((Boolean, Float) -> Unit)? = null

    private val density = resources.displayMetrics.density
    private val trackWidth = 4f * density
    private val thumbWidth = 12f * density
    private val thumbMinHeight = 44f * density
    private val gutter = 10f * density

    private val trackPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = 0x66B6A58D
    }
    private val thumbPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = 0xFF1E3A5F.toInt()
    }

    private var scrollOffset = 0
    private var scrollExtent = 0
    private var scrollRange = 0
    private var dragFraction = 1f
    private var dragActive = false

    fun updateScrollMetrics(offset: Int, extent: Int, range: Int) {
        scrollOffset = offset.coerceAtLeast(0)
        scrollExtent = extent.coerceAtLeast(0)
        scrollRange = range.coerceAtLeast(scrollExtent)
        if (!dragActive) {
            dragFraction = currentScrollFraction()
        }
        invalidate()
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val top = paddingTop.toFloat() + gutter
        val bottom = height - paddingBottom.toFloat() - gutter
        val actualHeight = bottom - top
        val maxScroll = scrollRange - scrollExtent
        if (maxScroll <= 0 || actualHeight <= thumbMinHeight / 2f) {
            return
        }

        val left = width - thumbWidth
        val centerX = left + thumbWidth / 2f
        val usableHeight = actualHeight.coerceAtLeast(thumbMinHeight)

        val trackRect = RectF(
            centerX - trackWidth / 2f,
            top,
            centerX + trackWidth / 2f,
            bottom
        )
        canvas.drawRoundRect(trackRect, trackWidth, trackWidth, trackPaint)

        val thumbHeight = max(thumbMinHeight, usableHeight * (scrollExtent.toFloat() / scrollRange.toFloat()))
            .coerceAtMost(usableHeight)
        val maxThumbTop = (bottom - thumbHeight).coerceAtLeast(top)
        val thumbTop = (top + (usableHeight - thumbHeight) * dragFraction).coerceIn(top, maxThumbTop)
        val thumbRect = RectF(
            left,
            thumbTop,
            left + thumbWidth,
            thumbTop + thumbHeight
        )
        thumbPaint.color = if (dragActive) 0xFF0F2743.toInt() else 0xCC1E3A5F.toInt()
        canvas.drawRoundRect(thumbRect, thumbWidth, thumbWidth, thumbPaint)
    }

    override fun onTouchEvent(event: MotionEvent): Boolean {
        if (scrollRange - scrollExtent <= 0) {
            return false
        }
        return when (event.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                parent?.requestDisallowInterceptTouchEvent(true)
                updateDrag(event.y)
                true
            }

            MotionEvent.ACTION_MOVE -> {
                updateDrag(event.y)
                true
            }

            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                updateDrag(event.y)
                dragActive = false
                onDragStateChanged?.invoke(false, dragFraction)
                invalidate()
                true
            }

            else -> super.onTouchEvent(event)
        }
    }

    private fun updateDrag(y: Float) {
        val top = paddingTop.toFloat() + gutter
        val bottom = height - paddingBottom.toFloat() - gutter
        val usableHeight = (bottom - top).coerceAtLeast(1f)
        dragActive = true
        dragFraction = ((y - top) / usableHeight).coerceIn(0f, 1f)
        onDragStateChanged?.invoke(true, dragFraction)
        onSeekChanged?.invoke(dragFraction)
        invalidate()
    }

    private fun currentScrollFraction(): Float {
        val maxScroll = scrollRange - scrollExtent
        if (maxScroll <= 0) {
            return 1f
        }
        return (scrollOffset.toFloat() / maxScroll.toFloat()).coerceIn(0f, 1f)
    }
}
