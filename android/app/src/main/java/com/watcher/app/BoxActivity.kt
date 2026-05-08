package com.watcher.app

import android.os.Bundle
import android.view.View
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.viewpager2.widget.ViewPager2
import com.google.android.material.tabs.TabLayout
import com.google.android.material.tabs.TabLayoutMediator

class BoxActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var titleText: TextView
    private lateinit var statusText: TextView
    private lateinit var pagerAdapter: BoxPagerAdapter
    private lateinit var sourceAdapter: BoxSourceAdapter
    private lateinit var sourceRecycler: RecyclerView
    private lateinit var pager: ViewPager2
    private var tabLayout: TabLayout? = null
    private var mediator: TabLayoutMediator? = null
    private var sourceId: String = ""

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_box)
        installSystemBarInsets(findViewById(R.id.boxRoot))

        api = WatcherApi(this)
        sourceId = intent.getStringExtra(EXTRA_SOURCE_ID).orEmpty()
        titleText = findViewById(R.id.boxTitleText)
        statusText = findViewById(R.id.boxStatusText)
        tabLayout = findViewById(R.id.boxTabLayout)
        sourceRecycler = findViewById(R.id.boxSourceRecycler)
        sourceAdapter = BoxSourceAdapter { entry -> openSource(entry) }
        sourceRecycler.layoutManager = LinearLayoutManager(this)
        sourceRecycler.adapter = sourceAdapter

        pager = findViewById(R.id.boxPager)
        pagerAdapter = BoxPagerAdapter(this)
        pager.adapter = pagerAdapter
    }

    override fun onResume() {
        super.onResume()
        loadData()
    }

    private fun loadData() {
        if (sourceId.isBlank()) {
            loadSourceIndex()
        } else {
            loadSourceDetail(sourceId)
        }
    }

    private fun loadSourceIndex() {
        showSourceIndex()
        titleText.text = "Box"
        statusText.text = watcherText("加载信息源…", "Loading sources…")
        Thread {
            try {
                val adapters = api.fetchBoxAdapters()
                    .filter { it.kind == "box" || "catalog" in it.queryTypes }
                if (adapters.isEmpty()) {
                    runOnUiThread {
                        statusText.text = watcherText("没有 Box 配置", "No box config")
                    }
                    return@Thread
                }

                val entries = mutableListOf<BoxSourceEntry>()
                for (adapter in adapters) {
                    val entry = try {
                        val catalog = api.fetchBoxCatalog(adapter.id)
                        BoxSourceEntry(adapter, catalog)
                    } catch (exc: Exception) {
                        BoxSourceEntry(adapter, null, shortError(exc.message))
                    }
                    entries += entry
                }

                runOnUiThread {
                    sourceAdapter.submit(entries)
                    val available = entries.count { it.catalog != null && it.error.isBlank() }
                    val totalViews = entries.sumOf { it.catalog?.views?.size ?: 0 }
                    val failures = entries.size - available
                    val suffix = if (failures > 0) watcherText(" · ${failures} 个源失败", " · ${failures} failed") else ""
                    statusText.text = watcherText(
                        "$available 个源 · $totalViews 个视图$suffix",
                        "$available sources · $totalViews views$suffix"
                    )
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = watcherText("加载失败：${exc.message}", "Failed: ${exc.message}")
                }
            }
        }.start()
    }

    private fun loadSourceDetail(adapterId: String) {
        showSourceDetail()
        titleText.text = intent.getStringExtra(EXTRA_SOURCE_TITLE).orEmpty().ifBlank { adapterId }
        statusText.text = watcherText("加载视图…", "Loading views…")
        Thread {
            try {
                val catalog = api.fetchBoxCatalog(adapterId)
                val pages = mutableListOf<BoxDatasetResult>()
                for (view in selectedViews(catalog)) {
                    val dataset = catalog.datasets.firstOrNull {
                        it.id == view.datasetId || it.viewId == view.id || it.name == view.datasetId
                    }
                    val datasetName = dataset?.name?.ifBlank { dataset.id } ?: view.datasetId
                    if (datasetName.isBlank()) continue
                    val loaded = api.fetchBoxDataset(adapterId = adapterId, name = datasetName, limit = 300)
                    val loadedView = loaded.view ?: view
                    val displayView = loadedView.copy(
                        title = loadedView.title.ifBlank { view.title.ifBlank { dataset?.title ?: datasetName } },
                        datasetId = loadedView.datasetId.ifBlank { view.datasetId },
                        groupBy = loadedView.groupBy.ifBlank { view.groupBy }
                    )
                    pages += loaded.copy(
                        name = datasetName,
                        view = displayView
                    )
                }
                runOnUiThread {
                    if (catalog.title.isNotBlank()) {
                        titleText.text = catalog.title
                    }
                    if (pages.isEmpty()) {
                        pagerAdapter.setPages(emptyList())
                        setupTabs(emptyList())
                        statusText.text = watcherText("这个源没有可展示视图", "No displayable views")
                        return@runOnUiThread
                    }
                    pagerAdapter.setPages(pages)
                    setupTabs(pages)
                    val total = pages.sumOf { it.records.size }
                    statusText.text = watcherText(
                        "${pages.size} 个视图 · $total 条",
                        "${pages.size} views · $total records"
                    )
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = watcherText("加载失败：${shortError(exc.message)}", "Failed: ${shortError(exc.message)}")
                }
            }
        }.start()
    }

    private fun selectedViews(catalog: BoxCatalog): List<BoxDatasetView> {
        val preferred = catalog.defaultViews
            .mapNotNull { viewId -> catalog.views.firstOrNull { it.id == viewId } }
        return preferred.ifEmpty { catalog.views }
    }

    private fun setupTabs(pages: List<BoxDatasetResult>) {
        mediator?.detach()
        val tabs = tabLayout ?: return
        mediator = TabLayoutMediator(tabs, pager) { tab, position ->
            tab.text = pageLabel(pages[position])
        }
        mediator?.attach()
    }

    private fun pageLabel(page: BoxDatasetResult): String {
        return page.view?.title?.ifBlank { page.name }?.take(14) ?: page.name.take(14)
    }

    private fun openSource(entry: BoxSourceEntry) {
        startActivity(
            android.content.Intent(this, BoxActivity::class.java)
                .putExtra(EXTRA_SOURCE_ID, entry.adapter.id)
                .putExtra(EXTRA_SOURCE_TITLE, entry.catalog?.title?.ifBlank { entry.adapter.title } ?: entry.adapter.title)
        )
    }

    private fun showSourceIndex() {
        mediator?.detach()
        sourceRecycler.visibility = View.VISIBLE
        tabLayout?.visibility = View.GONE
        pager.visibility = View.GONE
    }

    private fun showSourceDetail() {
        sourceRecycler.visibility = View.GONE
        tabLayout?.visibility = View.VISIBLE
        pager.visibility = View.VISIBLE
    }

    companion object {
        const val EXTRA_SOURCE_ID = "box_source_id"
        const val EXTRA_SOURCE_TITLE = "box_source_title"
    }

    private fun shortError(message: String?): String {
        val text = message?.trim().orEmpty()
        if (text.isBlank()) return "failed"
        return if (text.length > 80) text.take(77) + "..." else text
    }
}
