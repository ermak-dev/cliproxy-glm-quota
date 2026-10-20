package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// addCredentialPath is the resource path: the host auto-namespaces
// ResourceRoute under /v0/resource/plugins/<id>/, so "/add" alone becomes
// /v0/resource/plugins/glm-quota/add -- reachable from the panel's "GLM
// Coding Plan" nav entry (its Menu label).
const addCredentialPath = "/add"

// addCredentialManagementPath is the save route. Unlike ResourceRoute,
// ManagementRoute gets NO automatic per-plugin namespacing (confirmed from
// internal/pluginhost/management.go's normalizeManagementRoute, which just
// prepends the bare "/v0/management" base path) -- a bare "/add" here would
// collide with every other plugin's "/add" and, worse, land on whichever one
// registered first. The "/plugins/<id>/" prefix is the convention the host
// itself documents for this (legacyPluginRoutePrefix in the same file), so
// this plugin supplies it explicitly rather than relying on it being added.
const addCredentialManagementPath = "/plugins/" + pluginIdentifier + "/add"

// addCredentialManagementURL is where the form's own script actually
// fetch()es -- the host's management base path plus the route above, kept
// as one constant so the two can't silently drift apart.
const addCredentialManagementURL = "/v0/management" + addCredentialManagementPath

// The form page is a ResourceRoute (GET, unauthenticated -- confirmed
// against a running v8.0.16 host that a resource path only ever routes GET;
// POST 404s before the plugin is even called, which is why the save action
// below is a separate, authenticated ManagementRoute instead). Viewing the
// empty form needs no secret; the page's own inline script asks for the
// management key only when actually submitting, and sends it as a real
// Authorization header to the authenticated route -- never as a URL query
// string, so neither key ever lands in an access log or browser history.
func handleManagementRegister() ([]byte, error) {
	return okEnvelope(managementRegistrationResponse{
		Resources: []resourceRoute{
			{
				Path:        addCredentialPath,
				Menu:        "GLM Coding Plan",
				Description: "Add or update the API key used for GLM Coding Plan quota monitoring.",
			},
		},
		Routes: []managementRoute{
			{Method: http.MethodPost, Path: addCredentialManagementPath},
		},
	})
}

func handleManagementHandle(request []byte) ([]byte, error) {
	var req managementRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, fmt.Errorf("decode management.handle request: %w", err)
	}

	switch req.Method {
	case http.MethodPost:
		return handleAddCredentialSubmit(req.Body)
	default:
		// Only one resource is registered, and the host dispatches to it by
		// path already; any method other than POST is someone looking at
		// the form, not submitting it.
		return okEnvelope(htmlPage(http.StatusOK, renderAddCredentialForm("")))
	}
}

func handleAddCredentialSubmit(body []byte) ([]byte, error) {
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return okEnvelope(htmlPage(http.StatusBadRequest, renderAddCredentialForm("Could not read the submitted form.")))
	}

	apiKey := strings.TrimSpace(form.Get("api_key"))
	if apiKey == "" {
		return okEnvelope(htmlPage(http.StatusBadRequest, renderAddCredentialForm("The API key field is required.")))
	}
	label := strings.TrimSpace(form.Get("label"))
	if label == "" {
		label = "GLM Coding Plan"
	}

	// The slug becomes the saved file's name, which main.go's
	// credentialIDFromFileName turns into the credential's stable ID.
	// Two submissions with the same slug intentionally replace each other
	// (rotating a key); two different slugs become two separate quota
	// cards -- this is what "more than one GLM key" means in practice.
	slug := slugify(form.Get("name"))
	if slug == "" {
		slug = slugify(label)
	}
	if slug == "" {
		slug = "default"
	}
	fileName := fmt.Sprintf("%s-%s.json", pluginIdentifier, slug)

	fileContent, errMarshal := json.Marshal(map[string]string{
		"type":    pluginIdentifier,
		"api_key": apiKey,
		"label":   label,
	})
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal credential file: %w", errMarshal)
	}

	saveReq, errMarshal := json.Marshal(hostAuthSaveRequest{
		Name: fileName,
		JSON: fileContent,
	})
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal host.auth.save request: %w", errMarshal)
	}

	if _, errSave := callHost(methodHostAuthSave, saveReq); errSave != nil {
		return okEnvelope(htmlPage(http.StatusBadGateway, renderAddCredentialForm(fmt.Sprintf("Save failed: %s", errSave.Error()))))
	}

	return okEnvelope(htmlPage(http.StatusOK, addCredentialSuccessPage()))
}

func htmlPage(status int, body string) managementResponse {
	return managementResponse{
		StatusCode: status,
		Headers:    map[string][]string{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       []byte(body),
	}
}

func renderAddCredentialForm(errMessage string) string {
	var errBlock string
	if errMessage != "" {
		errBlock = fmt.Sprintf(`<p id="err" style="color:#b00020">%s</p>`, htmlEscape(errMessage))
	} else {
		errBlock = `<p id="err" style="color:#b00020"></p>`
	}
	// This page is served from the unauthenticated resource path (so it can
	// be opened with nothing but admin-panel access), but the save action
	// is the authenticated management route -- so the script below, not a
	// plain <form method=POST>, does the submit: a browser form post can't
	// attach an Authorization header, only fetch()/XHR can.
	return fmt.Sprintf(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>GLM Coding Plan</title></head>
<body style="font-family:system-ui,sans-serif;max-width:480px;margin:40px auto;line-height:1.4">
<h2>GLM Coding Plan — quota credential</h2>
<p>Saves a small, separate credential (type <code>glm-quota</code>) used only to
read quota from <a href="https://z.ai/manage-apikey/coding-plan/personal/usage"
target="_blank" rel="noopener">z.ai</a>. This does not change how GLM
inference requests are routed, and does not touch any existing credential.</p>
<p>Submitting with a <strong>new</strong> name adds another key, shown as its
own quota card. Submitting again with the <strong>same</strong> name replaces
that key (e.g. after rotating it).</p>
%[1]s
<form id="f">
<label>Management key (the same one used to log into this panel)<br>
<input type="password" name="mgmt_key" required autocomplete="off" style="width:100%%"></label>
<br><br>
<label>Name (unique per key, e.g. "personal" or "team-2")<br>
<input type="text" name="name" placeholder="default" style="width:100%%"></label>
<br><br>
<label>API key<br>
<input type="password" name="api_key" required autocomplete="off" style="width:100%%"></label>
<br><br>
<label>Label (optional, shown in the credentials list)<br>
<input type="text" name="label" placeholder="GLM Coding Plan" style="width:100%%"></label>
<br><br>
<button type="submit">Save</button>
</form>
<script>
document.getElementById("f").addEventListener("submit", async function (ev) {
  ev.preventDefault();
  var form = ev.target;
  var mgmtKey = form.mgmt_key.value;
  var body = new URLSearchParams();
  body.set("name", form.name.value);
  body.set("api_key", form.api_key.value);
  body.set("label", form.label.value);
  var err = document.getElementById("err");
  err.textContent = "";
  try {
    var resp = await fetch("%[2]s", {
      method: "POST",
      headers: {
        "Authorization": "Bearer " + mgmtKey,
        "Content-Type": "application/x-www-form-urlencoded"
      },
      body: body.toString()
    });
    var text = await resp.text();
    if (resp.ok) {
      document.open(); document.write(text); document.close();
    } else {
      err.textContent = "Save failed (HTTP " + resp.status + "): " + text;
    }
  } catch (e) {
    err.textContent = "Request failed: " + e;
  }
});
</script>
</body>
</html>`, errBlock, addCredentialManagementURL)
}

func addCredentialSuccessPage() string {
	return `<!doctype html>
<html>
<head><meta charset="utf-8"><title>GLM Coding Plan</title></head>
<body style="font-family:system-ui,sans-serif;max-width:480px;margin:40px auto;line-height:1.4">
<h2>Saved</h2>
<p>The glm-quota credential was saved. Open the Quota tab to see it; if the
card doesn't appear right away, reopen the credentials list once.</p>
</body>
</html>`
}

// slugify turns an operator-entered name into a safe auth-dir file-name
// fragment: lowercase ASCII letters, digits and hyphens only, no leading,
// trailing or repeated hyphens. An input with no safe characters at all
// (e.g. empty, or pure punctuation) returns "".
func slugify(s string) string {
	var b strings.Builder
	lastHyphen := true // true keeps a leading hyphen from ever being written
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func htmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&#34;",
		"'", "&#39;",
	)
	return replacer.Replace(s)
}
