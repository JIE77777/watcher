package httpapi

import (
	"html/template"
	"io"
	"strings"

	"watcher/internal/model"
	"watcher/internal/runner"
)

type Flash struct {
	Level   string
	Message string
}

type DashboardData struct {
	Flash              Flash
	Tasks              []model.WatchTask
	Events             []model.WatcherTaskEvent
	Tools              []runner.ToolManifest
	RunningTaskIDs     map[string]bool
	DefaultToolConfig  string
	DefaultRuleOptions string
	RelayConfigured    bool
	OwnerTokenSet      bool
}

type LoginData struct {
	Flash         Flash
	OwnerTokenSet bool
}

var dashboardTemplate = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"join": strings.Join,
	"taskRunning": func(taskID string, running map[string]bool) bool {
		return running[taskID]
	},
	"deliveryNames": func(targets []model.DeliveryTarget) string {
		names := make([]string, 0, len(targets))
		for _, target := range targets {
			names = append(names, string(target.Type))
		}
		return strings.Join(names, ", ")
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Watcher Terminal</title>
  <style>
    :root {
      --ink: #132238;
      --muted: #5c6b80;
      --line: rgba(19, 34, 56, 0.12);
      --paper: rgba(255, 255, 255, 0.92);
      --accent: #da5a2a;
      --accent-soft: rgba(218, 90, 42, 0.12);
      --green: #0f8a5f;
      --red: #b53f3f;
      --bg-a: #f2efe7;
      --bg-b: #dbe7f4;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Segoe UI", "PingFang SC", "Noto Sans SC", sans-serif;
      color: var(--ink);
      background:
        radial-gradient(circle at top left, rgba(218, 90, 42, 0.15), transparent 30%),
        linear-gradient(135deg, var(--bg-a), var(--bg-b));
      min-height: 100vh;
    }
    .shell {
      width: min(1180px, calc(100vw - 32px));
      margin: 24px auto;
    }
    .hero {
      background: linear-gradient(135deg, rgba(19, 34, 56, 0.96), rgba(29, 52, 81, 0.92));
      color: white;
      border-radius: 24px;
      padding: 28px;
      box-shadow: 0 20px 60px rgba(19, 34, 56, 0.18);
      display: grid;
      grid-template-columns: 1.4fr 1fr;
      gap: 20px;
    }
    .hero h1 {
      margin: 0 0 10px;
      font-size: clamp(28px, 5vw, 48px);
      line-height: 0.95;
      letter-spacing: -0.04em;
    }
    .hero p {
      margin: 0;
      max-width: 58ch;
      color: rgba(255, 255, 255, 0.78);
      line-height: 1.55;
    }
    .stats {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 12px;
      align-self: stretch;
    }
    .stat {
      border-radius: 18px;
      padding: 18px;
      background: rgba(255, 255, 255, 0.08);
      border: 1px solid rgba(255, 255, 255, 0.1);
      backdrop-filter: blur(8px);
    }
    .stat b {
      display: block;
      font-size: 28px;
      margin-top: 8px;
    }
    .flash {
      margin-top: 16px;
      border-radius: 16px;
      padding: 12px 14px;
      font-weight: 600;
    }
    .flash.info { background: rgba(15, 138, 95, 0.12); color: var(--green); }
    .flash.error { background: rgba(181, 63, 63, 0.12); color: var(--red); }
    .grid {
      margin-top: 18px;
      display: grid;
      grid-template-columns: 1.2fr 0.8fr;
      gap: 18px;
    }
    .card {
      background: var(--paper);
      border: 1px solid var(--line);
      border-radius: 22px;
      padding: 18px;
      box-shadow: 0 12px 32px rgba(19, 34, 56, 0.08);
    }
    .card h2 {
      margin: 0 0 8px;
      font-size: 20px;
      letter-spacing: -0.02em;
    }
    .subtle {
      color: var(--muted);
      font-size: 14px;
      line-height: 1.55;
    }
    .task-list,
    .event-list,
    .tool-list {
      display: grid;
      gap: 12px;
      margin-top: 16px;
    }
    .task, .event, .tool {
      border: 1px solid var(--line);
      border-radius: 18px;
      padding: 14px;
      background: rgba(255,255,255,0.8);
    }
    .row {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: flex-start;
      flex-wrap: wrap;
    }
    .pill-row {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      margin-top: 10px;
    }
    .pill {
      display: inline-flex;
      align-items: center;
      padding: 5px 10px;
      border-radius: 999px;
      font-size: 12px;
      background: var(--accent-soft);
      color: var(--accent);
      border: 1px solid rgba(218, 90, 42, 0.16);
    }
    .pill.ok { background: rgba(15, 138, 95, 0.1); color: var(--green); border-color: rgba(15, 138, 95, 0.16); }
    .pill.off { background: rgba(181, 63, 63, 0.1); color: var(--red); border-color: rgba(181, 63, 63, 0.14); }
    form.inline {
      display: inline-flex;
      gap: 8px;
      flex-wrap: wrap;
      margin: 0;
    }
    button, .button {
      appearance: none;
      border: 0;
      border-radius: 999px;
      padding: 10px 14px;
      background: var(--ink);
      color: white;
      font: inherit;
      font-weight: 700;
      cursor: pointer;
      text-decoration: none;
    }
    button.secondary, .button.secondary {
      background: rgba(19, 34, 56, 0.08);
      color: var(--ink);
    }
    button.ghost {
      background: transparent;
      color: var(--ink);
      border: 1px solid var(--line);
    }
    label {
      display: block;
      font-size: 13px;
      color: var(--muted);
      margin-bottom: 6px;
    }
    input[type="text"], input[type="number"], textarea, select {
      width: 100%;
      border: 1px solid var(--line);
      border-radius: 14px;
      padding: 12px 14px;
      font: inherit;
      background: rgba(255,255,255,0.86);
      color: var(--ink);
    }
    textarea {
      min-height: 120px;
      resize: vertical;
    }
    .form-grid {
      display: grid;
      gap: 12px;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      margin-top: 16px;
    }
    .form-grid .full {
      grid-column: 1 / -1;
    }
    .check-row {
      display: flex;
      gap: 14px;
      flex-wrap: wrap;
      margin-top: 8px;
    }
    .check {
      display: inline-flex;
      gap: 8px;
      align-items: center;
      color: var(--ink);
      font-size: 14px;
    }
    .muted-block {
      margin-top: 12px;
      padding: 12px;
      border-radius: 16px;
      background: rgba(19, 34, 56, 0.04);
      color: var(--muted);
      font-size: 13px;
      line-height: 1.55;
    }
    details summary {
      cursor: pointer;
      font-weight: 700;
    }
    pre {
      margin: 10px 0 0;
      padding: 12px;
      border-radius: 14px;
      background: rgba(19, 34, 56, 0.06);
      white-space: pre-wrap;
      word-break: break-word;
      font-family: "JetBrains Mono", "SFMono-Regular", monospace;
      font-size: 12px;
    }
    .hero-actions {
      display: flex;
      gap: 10px;
      flex-wrap: wrap;
      margin-top: 18px;
    }
    @media (max-width: 980px) {
      .hero, .grid { grid-template-columns: 1fr; }
      .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
      .form-grid { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <main class="shell">
    <section class="hero">
      <div>
        <h1>Watcher<br>Terminal</h1>
        <p>The local control room for your task feed, task runner, and tool chain. Create watchers, trigger runs, and inspect recent typed task events without dropping back to raw curl.</p>
        <div class="hero-actions">
          <a class="button secondary" href="/api/v2/modules/box/events">Task Feed JSON</a>
          <a class="button secondary" href="/api/v1/tools">Tools JSON</a>
          {{if .OwnerTokenSet}}
          <form class="inline" action="/logout" method="post">
            <button class="ghost" type="submit">Log Out</button>
          </form>
          {{end}}
        </div>
      </div>
      <div class="stats">
        <div class="stat"><span>Configured tasks</span><b>{{len .Tasks}}</b></div>
        <div class="stat"><span>Recent task events</span><b>{{len .Events}}</b></div>
        <div class="stat"><span>Available tools</span><b>{{len .Tools}}</b></div>
        <div class="stat"><span>Relay</span><b>{{if .RelayConfigured}}Ready{{else}}Local only{{end}}</b></div>
      </div>
    </section>

    {{if .Flash.Message}}
    <div class="flash {{.Flash.Level}}">{{.Flash.Message}}</div>
    {{end}}

    <section class="grid">
      <div>
        <section class="card">
          <h2>Tasks</h2>
          <div class="subtle">Run, pause, and inspect active watchers. Scheduler state comes from the local service database.</div>
          <div class="task-list">
            {{range .Tasks}}
            <article class="task">
              <div class="row">
                <div>
                  <strong>{{.Name}}</strong>
                  <div class="subtle">Tool: {{.Tool}} · every {{.ScheduleSeconds}}s</div>
                  <div class="pill-row">
                    {{if .Enabled}}<span class="pill ok">enabled</span>{{else}}<span class="pill off">disabled</span>{{end}}
                    {{if taskRunning .ID $.RunningTaskIDs}}<span class="pill">running</span>{{end}}
                    {{if .LastStatus}}<span class="pill">{{.LastStatus}}</span>{{end}}
                    {{if .Labels}}<span class="pill">{{join .Labels ", "}}</span>{{end}}
                    {{if .DeliveryTargets}}<span class="pill">{{deliveryNames .DeliveryTargets}}</span>{{end}}
                  </div>
                </div>
                <div>
                  <form class="inline" action="/ui/tasks/{{.ID}}/run" method="post">
                    <button type="submit">Run Now</button>
                  </form>
                  <form class="inline" action="/ui/tasks/{{.ID}}/toggle" method="post">
                    <button class="secondary" type="submit">{{if .Enabled}}Disable{{else}}Enable{{end}}</button>
                  </form>
                </div>
              </div>
              <div class="muted-block">
                Last run: {{if .LastRunAt}}{{.LastRunAt}}{{else}}never{{end}}<br>
                {{if .LastError}}Last error: {{.LastError}}{{else}}Last error: none{{end}}
              </div>
            </article>
            {{else}}
            <div class="muted-block">No tasks yet. Create the first one in the form on the right.</div>
            {{end}}
          </div>
        </section>

        <section class="card" style="margin-top: 18px;">
          <h2>Watcher Task Feed</h2>
          <div class="subtle">Recent typed watcher.task events generated by task diffs. Expand any item to inspect the full body.</div>
          <div class="event-list">
            {{range .Events}}
            <article class="event">
              <div class="row">
                <div>
                  <strong>{{.DisplayTitle}}</strong>
                  <div class="subtle">{{.OccurredAt}} · {{.Severity}}</div>
                </div>
                {{if .Labels}}
                <div class="pill-row">{{range .Labels}}<span class="pill">{{.}}</span>{{end}}</div>
                {{end}}
              </div>
              <details>
                <summary>{{.Summary}}</summary>
                <pre>{{.Body}}</pre>
              </details>
            </article>
            {{else}}
            <div class="muted-block">Task feed is empty. Run a task to generate the first watcher.task event.</div>
            {{end}}
          </div>
        </section>
      </div>

      <div>
        <section class="card">
          <h2>Create Task</h2>
          <div class="subtle">A quick browser-side form for the core workflow. Complex tools can still use the JSON API later.</div>
          <form action="/ui/tasks" method="post">
            <div class="form-grid">
              <div>
                <label for="name">Task name</label>
                <input id="name" name="name" type="text" placeholder="Example test7" required>
              </div>
              <div>
                <label for="tool">Tool</label>
                <select id="tool" name="tool" required>
                  {{range .Tools}}
                  <option value="{{.ID}}">{{.Name}} ({{.ID}})</option>
                  {{end}}
                </select>
              </div>
              <div>
                <label for="schedule_seconds">Schedule seconds</label>
                <input id="schedule_seconds" name="schedule_seconds" type="number" min="0" step="1" value="120">
              </div>
              <div>
                <label for="labels">Labels</label>
                <input id="labels" name="labels" type="text" placeholder="example, contest">
              </div>
              <div class="full">
                <label>Delivery targets</label>
                <div class="check-row">
                  <label class="check"><input type="checkbox" name="delivery_desktop" checked> desktop</label>
                  <label class="check"><input type="checkbox" name="delivery_relay"> relay_push</label>
                  <label class="check"><input type="checkbox" name="delivery_webhook"> webhook</label>
                  <label class="check"><input type="checkbox" name="enabled" checked> enabled</label>
                </div>
              </div>
              <div class="full">
                <label for="webhook_url">Webhook URL</label>
                <input id="webhook_url" name="webhook_url" type="text" placeholder="https://example.com/webhook">
              </div>
              <div class="full">
                <label for="tool_config">Tool config JSON</label>
                <textarea id="tool_config" name="tool_config">{{.DefaultToolConfig}}</textarea>
              </div>
              <div class="full">
                <label for="rule_options">Rule options JSON</label>
                <textarea id="rule_options" name="rule_options">{{.DefaultRuleOptions}}</textarea>
              </div>
            </div>
            <div class="hero-actions">
              <button type="submit">Create Task</button>
            </div>
          </form>
        </section>

        <section class="card" style="margin-top: 18px;">
          <h2>Tools</h2>
          <div class="subtle">Runtime tools discovered from the project tools directory.</div>
          <div class="tool-list">
            {{range .Tools}}
            <article class="tool">
              <strong>{{.Name}}</strong>
              <div class="subtle">{{.ID}} · {{.Kind}} · {{.Language}} · {{.Version}}</div>
              <div class="muted-block">{{.Description}}</div>
            </article>
            {{else}}
            <div class="muted-block">No tools discovered.</div>
            {{end}}
          </div>
        </section>
      </div>
    </section>
  </main>
</body>
</html>`))

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Watcher Login</title>
  <style>
    body {
      margin: 0;
      min-height: 100vh;
      display: grid;
      place-items: center;
      background: radial-gradient(circle at top, rgba(218, 90, 42, 0.12), transparent 35%), linear-gradient(135deg, #f1ece4, #d8e6f5);
      color: #132238;
      font-family: "Segoe UI", "PingFang SC", "Noto Sans SC", sans-serif;
    }
    .panel {
      width: min(460px, calc(100vw - 32px));
      padding: 28px;
      border-radius: 24px;
      background: rgba(255, 255, 255, 0.92);
      border: 1px solid rgba(19, 34, 56, 0.12);
      box-shadow: 0 20px 50px rgba(19, 34, 56, 0.1);
    }
    h1 { margin: 0 0 8px; font-size: 34px; letter-spacing: -0.04em; }
    p { margin: 0 0 16px; color: #5c6b80; line-height: 1.6; }
    label { display: block; font-size: 13px; color: #5c6b80; margin-bottom: 6px; }
    input {
      width: 100%;
      box-sizing: border-box;
      border: 1px solid rgba(19, 34, 56, 0.12);
      border-radius: 14px;
      padding: 12px 14px;
      font: inherit;
    }
    button {
      margin-top: 14px;
      width: 100%;
      border: 0;
      border-radius: 999px;
      padding: 12px 14px;
      background: #132238;
      color: white;
      font: inherit;
      font-weight: 700;
      cursor: pointer;
    }
    .flash {
      margin-bottom: 14px;
      border-radius: 14px;
      padding: 10px 12px;
      background: rgba(181, 63, 63, 0.12);
      color: #b53f3f;
      font-weight: 700;
    }
  </style>
</head>
<body>
  <main class="panel">
    <h1>Watcher</h1>
    <p>{{if .OwnerTokenSet}}Enter the local owner token to unlock the dashboard and API in this browser session.{{else}}No owner token is configured. You can access the dashboard directly.{{end}}</p>
    {{if .Flash.Message}}<div class="flash">{{.Flash.Message}}</div>{{end}}
    {{if .OwnerTokenSet}}
    <form action="/login" method="post">
      <label for="token">Owner token</label>
      <input id="token" name="token" type="password" autocomplete="off" required>
      <button type="submit">Open Dashboard</button>
    </form>
    {{else}}
    <form action="/" method="get"><button type="submit">Open Dashboard</button></form>
    {{end}}
  </main>
</body>
</html>`))

func RenderDashboard(w io.Writer, data DashboardData) error {
	return dashboardTemplate.Execute(w, data)
}

func RenderLogin(w io.Writer, data LoginData) error {
	return loginTemplate.Execute(w, data)
}
