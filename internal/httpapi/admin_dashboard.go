package httpapi

import (
	"context"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// AdminDiagnostics is everything the panel reports about the running process.
//
// It exists so an operator can answer "why did that upload go where it did" without
// reading logs. The panel is a development tool, and the failures it exists to surface
// — a fallback that was never wired, a consent gate that never opens, a backend whose
// credentials are rejected — are exactly the ones that are invisible from the outside.
type AdminDiagnostics struct {
	// FaultInjection reports whether Drive is being forced unavailable.
	FaultInjection bool
	// FallbackEnabled and FallbackVendors describe the configured destination.
	FallbackEnabled bool
	FallbackVendors []string
	// ConsentGranted is whether this tenant may route to the fallback at all.
	// It is false on a fresh install and is the most commonly missed step.
	ConsentGranted bool
	ConsentVersion string
	EverAsked      bool
	// Backends is one row per configured fallback provider, with the result of a
	// real reachability check rather than the configuration being merely present.
	Backends []AdminBackendStatus
	// Objects counts what this tenant has stored, by provider.
	ObjectCount int
	ReadyCount  int
}

// AdminBackendStatus is one fallback provider's live status.
type AdminBackendStatus struct {
	ID     string
	Vendor string
	Bucket string
	// Reachable is the result of an actual provider call made for this page load.
	Reachable bool
	// Detail explains a failure in terms an operator can act on.
	Detail string
}

// DiagnosticsProvider supplies the panel with live status.
type DiagnosticsProvider interface {
	Diagnostics(ctx context.Context) AdminDiagnostics
}

// renderAdminDashboard draws the page.
func renderAdminDashboard(w http.ResponseWriter, data adminDashboardData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := adminDashboardTemplate.Execute(w, data); err != nil {
		http.Error(w, "admin page unavailable", http.StatusInternalServerError)
	}
}

// adminDashboardData is everything the template reads.
type adminDashboardData struct {
	SignedIn   bool
	TenantID   string
	UserID     string
	Status     *adminStatusView
	Diag       AdminDiagnostics
	Now        time.Time
	ConsentURL string
}

type adminStatusView struct {
	Connected      bool
	Status         string
	Subject        string
	GrantedScopes  []string
	FolderPresent  bool
	LastSuccessAt  *time.Time
	FallbackReason string
}

var adminDashboardTemplate = template.Must(template.New("admin").Funcs(template.FuncMap{
	"join": strings.Join,
	"tick": func(b bool) string {
		if b {
			return "ok"
		}
		return "bad"
	},
	"yesno": func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	},
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'">
<title>Storvia admin</title><style>
body{font:15px/1.55 system-ui,-apple-system,sans-serif;max-width:900px;margin:2.5rem auto;padding:0 1rem;color:#17202a}
main{border:1px solid #d5dbe0;border-radius:12px;padding:1.75rem 2rem}
h1{font-size:1.35rem;margin:0 0 .25rem}h2{font-size:1rem;margin:2rem 0 .5rem;text-transform:uppercase;letter-spacing:.04em;color:#59636e}
code{background:#eef1f4;padding:.1rem .35rem;border-radius:4px;font-size:.9em}
.ok{color:#147d42;font-weight:600}.bad{color:#b3261e;font-weight:600}.warn{color:#9a5b00;font-weight:600}
button,a.button{border:0;border-radius:6px;padding:.55rem .9rem;background:#1769aa;color:#fff;text-decoration:none;font:inherit;cursor:pointer}
form{display:inline-block;margin:0 .5rem .5rem 0}
table{border-collapse:collapse;width:100%;margin:.5rem 0}
th,td{text-align:left;padding:.45rem .5rem;border-bottom:1px solid #eceff1;font-size:.93em;vertical-align:top}
th{color:#59636e;font-weight:600;width:11rem}
.banner{border-radius:8px;padding:.7rem .9rem;margin:1rem 0}
.banner.warn{background:#fff6e5;border:1px solid #f0d9a8}
.banner.info{background:#eef4fb;border:1px solid #cddff0}
.muted{color:#59636e}
</style></head><body><main>
<h1>Storvia development admin</h1>
<p class="muted">Development-only testing surface. It exercises the real code paths against the real providers.</p>

{{if .Diag.FaultInjection}}
<div class="banner warn"><strong>Drive outage simulation is ACTIVE.</strong> Drive's availability preflight is being forced to fail, so every new upload routes to the fallback provider. Turn off <code>admin_panel.simulate_drive_outage</code> to test the normal Drive path.</div>
{{end}}

{{if not .SignedIn}}
<h2>Google Drive</h2>
<p>Connect a test account. Storvia requests only the narrow <code>drive.file</code> scope.</p>
<p><a class="button" href="/admin/connect">Sign in with Google Drive</a></p>
<hr style="border:0;border-top:1px solid #eceff1;margin:1.5rem 0">
{{template "diagnostics" .}}
{{else}}
<h2>Drive connection</h2>
{{with .Status}}
<table>
<tr><th>Status</th><td class="{{tick .Connected}}">{{.Status}}</td></tr>
<tr><th>Google subject</th><td><code>{{if .Subject}}{{.Subject}}{{else}}not connected{{end}}</code></td></tr>
<tr><th>Granted scope</th><td><code>{{join .GrantedScopes ", "}}</code></td></tr>
<tr><th>Storvia folder</th><td>{{if .FolderPresent}}<span class="ok">present</span>{{else}}<span class="bad">not created yet</span>{{end}}</td></tr>
{{if not .FolderPresent}}
<tr><td colspan="2" style="padding-top:.75rem"><span class="muted">Every file Storvia stores goes in one Drive folder, so "delete everything Storvia holds" is a single action. It is created when you connect.</span><br><br>
<form method="post" action="/admin/ensure-folder"><button type="submit">Create or find the Storvia folder</button></form></td></tr>
{{end}}
{{if .LastSuccessAt}}<tr><th>Last Drive call</th><td>{{.LastSuccessAt}}</td></tr>{{end}}
</table>
{{end}}
<p><a class="button" href="/admin">Refresh</a></p>
<form method="post" action="/admin/disconnect"><button type="submit">Disconnect Drive</button></form>
<form method="post" action="/admin/logout"><button type="submit">Log out</button></form>
<hr style="border:0;border-top:1px solid #eceff1;margin:1.5rem 0">
{{template "diagnostics" .}}
{{end}}
</main></body></html>`))

// The diagnostics block is shared by the signed-in and signed-out pages so the
// fallback state is visible before connecting, which is where it is usually wrong.
const adminDiagnosticsBlock = `{{define "diagnostics"}}
<h2>Fallback</h2>
<table>
<tr><th>Enabled</th><td class="{{tick .Diag.FallbackEnabled}}">{{yesno .Diag.FallbackEnabled}}</td></tr>
<tr><th>Vendors</th><td>{{if .Diag.FallbackVendors}}<code>{{join .Diag.FallbackVendors ", "}}</code>{{else}}<span class="muted">none configured</span>{{end}}</td></tr>
<tr><th>Tenant consent</th><td>{{if .Diag.ConsentGranted}}<span class="ok">granted</span>{{else if .Diag.EverAsked}}<span class="warn">declined</span>{{else}}<span class="bad">never asked</span>{{end}}</td></tr>
<tr><th>Disclosure version</th><td><code>{{.Diag.ConsentVersion}}</code></td></tr>
</table>
{{if .Diag.FallbackEnabled}}
{{if not .Diag.ConsentGranted}}
<div class="banner warn"><strong>The fallback cannot fire.</strong> The routing policy requires this tenant to accept the outage-fallback disclosure. Configuration alone does not grant it &mdash; that is deliberate, because it moves files outside the user's own Drive.</div>
<form method="post" action="/admin/fallback-consent"><button type="submit">Accept fallback disclosure</button></form>
{{end}}
<h2>Backup providers</h2>
<table>
<tr><th>Provider</th><th>Reachable</th><th>Detail</th></tr>
{{range .Diag.Backends}}
<tr>
  <td><code>{{.ID}}</code><br><span class="muted">{{.Vendor}} &rarr; {{.Bucket}}</span></td>
  <td class="{{tick .Reachable}}">{{yesno .Reachable}}</td>
  <td>{{if .Detail}}<span class="muted">{{.Detail}}</span>{{else}}<span class="muted">&mdash;</span>{{end}}</td>
</tr>
{{end}}
</table>
{{end}}
<h2>Stored objects</h2>
<table>
<tr><th>Total</th><td>{{.Diag.ObjectCount}}</td></tr>
<tr><th>Ready</th><td>{{.Diag.ReadyCount}}</td></tr>
</table>
{{end}}`

func init() {
	template.Must(adminDashboardTemplate.Parse(adminDiagnosticsBlock))
}
