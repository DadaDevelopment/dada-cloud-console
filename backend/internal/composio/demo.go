package composio

import (
	"html/template"
	"net/http"
	"strings"
)

// demoPage is the one-button surface, rendered server-side so a demo needs no
// build step. It is a DEMO of the flow, not the product UI: the console's own
// Integrations page is a Next.js page that calls the same three endpoints.
var demoPage = template.Must(template.New("demo").Parse(`<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>DADA Cloud - Интеграции</title>
<style>
 body { font: 15px/1.5 -apple-system, Segoe UI, Roboto, sans-serif; margin: 0; padding: 32px; background: #0f1115; color: #e8eaed; }
 h1 { font-size: 20px; margin: 0 0 4px; }
 p.sub { color: #9aa0a6; margin: 0 0 24px; font-size: 13px; }
 .card { display: flex; align-items: center; gap: 14px; padding: 14px 16px; border: 1px solid #272b33; border-radius: 10px; margin-bottom: 10px; background: #15181e; max-width: 560px; }
 .card img { width: 28px; height: 28px; border-radius: 6px; background: #fff; }
 .name { flex: 1; font-weight: 500; }
 .state { font-size: 12px; color: #9aa0a6; margin-right: 4px; }
 .ok { color: #52c77e; }
 .pending { color: #d8a443; }
 button { font: inherit; padding: 7px 16px; border-radius: 7px; border: 0; cursor: pointer; background: #3b82f6; color: #fff; }
 button.ghost { background: #272b33; color: #e8eaed; }
 .who { font-size: 12px; color: #6b7178; margin-top: 24px; }
</style>
</head>
<body>
<h1>Интеграции</h1>
<p class="sub">Подключите сервис - и агент сможет работать в нём от вашего имени.</p>
<div id="list">загрузка...</div>
<div class="who">проект {{ .ProjectID }} &middot; пользователь {{ .EndUserKey }}</div>
<script>
const PROJECT = {{ .ProjectID }};
const USER = {{ .EndUserKey }};
const TOKEN = {{ .Token }};
const BASE = {{ .BasePath }};

function q(path, opts) {
  const sep = path.includes('?') ? '&' : '?';
  const url = BASE + path + (TOKEN ? sep + 'token=' + encodeURIComponent(TOKEN) : '');
  return fetch(url, opts).then(r => r.json());
}

async function render() {
  const [cat, mine] = await Promise.all([
    q('/integrations/catalog'),
    q('/integrations?project_id=' + PROJECT + '&end_user_key=' + encodeURIComponent(USER)),
  ]);
  const status = {};
  (mine.integrations || []).forEach(i => status[i.toolkit] = i.status);
  const list = document.getElementById('list');
  list.innerHTML = '';
  (cat.toolkits || []).forEach(t => {
    const st = status[t.slug];
    const card = document.createElement('div');
    card.className = 'card';
    const logo = document.createElement('img');
    logo.src = t.logo_url || '';
    const name = document.createElement('div');
    name.className = 'name';
    name.textContent = t.name;
    const state = document.createElement('span');
    state.className = 'state ' + (st === 'ACTIVE' ? 'ok' : st ? 'pending' : '');
    state.textContent = st === 'ACTIVE' ? 'подключено' : st ? 'не завершено' : '';
    const btn = document.createElement('button');
    btn.textContent = st === 'ACTIVE' ? 'Переподключить' : 'Подключить';
    if (st === 'ACTIVE') { btn.className = 'ghost'; }
    btn.onclick = async () => {
      btn.disabled = true;
      btn.textContent = 'открываю...';
      const res = await q('/integrations/connect', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ project_id: PROJECT, end_user_key: USER, toolkit: t.slug }),
      });
      if (res.redirect_url) { window.location = res.redirect_url; }
      else { btn.textContent = res.error || 'ошибка'; }
    };
    card.append(logo, name, state, btn);
    list.append(card);
  });
}
render();
</script>
</body>
</html>`))

// callbackPage is where Composio returns the user after the provider's consent
// screen. It exists so the loop closes visibly: the user lands back on our
// domain, not on a Composio page.
var callbackPage = template.Must(template.New("cb").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><title>Готово</title>
<style>body{font:15px/1.6 -apple-system,Segoe UI,Roboto,sans-serif;background:#0f1115;color:#e8eaed;padding:48px;text-align:center}a{color:#3b82f6}</style>
</head><body>
<h2>{{ if .OK }}Сервис подключён{{ else }}Подключение не завершено{{ end }}</h2>
<p>{{ if .OK }}Агент теперь может работать в нём от вашего имени.{{ else }}Можно попробовать снова.{{ end }}</p>
<p><a href="{{ .BackURL }}">Вернуться к интеграциям</a></p>
</body></html>`))

// demo renders the one-button page for one project and one end user.
func (s *Server) demo(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project_id"))
	endUser := strings.TrimSpace(r.URL.Query().Get("end_user_key"))
	if project == "" || endUser == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id and end_user_key are required"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = demoPage.Execute(w, map[string]any{
		"ProjectID":  project,
		"EndUserKey": endUser,
		"Token":      r.URL.Query().Get("token"),
		"BasePath":   s.basePath,
	})
}

// callback lands the user after authorization.
//
// Composio appends status and connected_account_id to whatever callback URL was
// passed, so the outcome is read off the query rather than polled. The local
// record is refreshed from Composio here, because the user just changed it on a
// page this platform does not host.
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	project := strings.TrimSpace(r.URL.Query().Get("project_id"))
	endUser := strings.TrimSpace(r.URL.Query().Get("end_user_key"))
	ok := strings.EqualFold(r.URL.Query().Get("status"), "success")

	if project != "" && endUser != "" {
		if projectID, err := parseUUID(project); err == nil {
			if _, err := s.svc.Sync(r.Context(), projectID, endUser); err != nil {
				logSyncFailure(project, endUser, err)
			}
		}
	}

	back := s.basePath + "/demo?project_id=" + project + "&end_user_key=" + endUser
	if token := r.URL.Query().Get("token"); token != "" {
		back += "&token=" + token
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = callbackPage.Execute(w, map[string]any{"OK": ok, "BackURL": back})
}
