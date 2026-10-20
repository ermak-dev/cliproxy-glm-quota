package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// addCredentialPath is the one resource this plugin registers. It is
// reachable under /v0/resource/plugins/glm-quota/add once the panel's
// "GLM Coding Plan" nav entry (its Menu label) is clicked.
const addCredentialPath = "/add"

// Resource routes are explicitly documented (sdk/pluginapi.ResourceRoute) as
// not management-authenticated -- the host serves them without checking the
// management Bearer key, unlike ManagementRoute. The alternative
// (ManagementRoute) would need the operator to also paste the management
// key into this form, which this plugin has no way to verify is correct
// itself; the host-level auth middleware is what actually checks it, and it
// runs ONLY in front of ManagementRoute, not ResourceRoute. Using
// ResourceRoute here matches the one official example that does the same
// kind of credential-writing form (examples/plugin/host-callback-auth-files)
// and relies on the same thing that example relies on: whoever can reach the
// Management Center's "Plugins" nav at all has already passed the
// deployment's own admin gate (typically a reverse proxy in front of the
// otherwise loopback-only management port). This plugin improves on that
// example in one respect: the API key travels in a POST body, never in a
// URL query string, so it never lands in an access log or browser history.
func handleManagementRegister() ([]byte, error) {
	return okEnvelope(managementRegistrationResponse{
		Resources: []resourceRoute{
			{
				Path:        addCredentialPath,
				Menu:        "GLM Coding Plan",
				Description: "Add or update the API key used for GLM Coding Plan quota monitoring.",
			},
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

	fileContent, errMarshal := json.Marshal(map[string]string{
		"type":    pluginIdentifier,
		"api_key": apiKey,
		"label":   label,
	})
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal credential file: %w", errMarshal)
	}

	saveReq, errMarshal := json.Marshal(hostAuthSaveRequest{
		Name: pluginIdentifier + ".json",
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
		errBlock = fmt.Sprintf(`<p style="color:#b00020">%s</p>`, htmlEscape(errMessage))
	}
	return fmt.Sprintf(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>GLM Coding Plan</title></head>
<body style="font-family:system-ui,sans-serif;max-width:480px;margin:40px auto;line-height:1.4">
<h2>GLM Coding Plan — quota credential</h2>
<p>Saves a small, separate credential (type <code>glm-quota</code>) used only to
read quota from <a href="https://z.ai/manage-apikey/coding-plan/personal/usage"
target="_blank" rel="noopener">z.ai</a>. This does not change how GLM
inference requests are routed, and does not touch any existing credential.</p>
%s
<form method="POST">
<label>API key<br>
<input type="password" name="api_key" required autocomplete="off" style="width:100%%"></label>
<br><br>
<label>Label (optional)<br>
<input type="text" name="label" placeholder="GLM Coding Plan" style="width:100%%"></label>
<br><br>
<button type="submit">Save</button>
</form>
</body>
</html>`, errBlock)
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
