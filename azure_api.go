package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

// ============================================================
// Azure OpenAI URL compatibility layer (downstream)
// Accepts the Azure OpenAI SDK URL style:
//   POST /openai/deployments/{deployment}/chat/completions?api-version=...
// The deployment name is the model; the request body is already OpenAI
// chat-completions compatible (it simply omits the "model" field because the
// deployment *is* the model in Azure semantics). We inject "model" and rewrite
// to the standard /v1/chat/completions gateway path. The response is already
// OpenAI-format, so no translation back is required.
// ============================================================

// azureAuthAdapter converts the Azure OpenAI SDK's "api-key" header into the
// "Authorization: Bearer" form expected by withProxyAuth.
func azureAuthAdapter(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if apiKey := r.Header.Get("api-key"); apiKey != "" {
				r.Header.Set("Authorization", "Bearer "+apiKey)
			}
		}
		handler(w, r)
	}
}

// handleAzureChatCompletions handles POST /openai/deployments/{deployment}/chat/completions.
func handleAzureChatCompletions(w http.ResponseWriter, r *http.Request) {
	deployment := r.PathValue("deployment")
	if deployment == "" {
		writeError(w, 400, "missing deployment name in path")
		return
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxGatewayBodySize+1))
	if err != nil {
		writeError(w, 400, "failed to read request body")
		return
	}
	if len(bodyBytes) > maxGatewayBodySize {
		writeError(w, 413, "request body too large")
		return
	}
	r.Body.Close()

	// Inject the deployment name as the model. Azure chat requests omit "model"
	// because the deployment already identifies the model; we make it explicit so
	// the existing gateway routing can select a provider.
	newBody, err := injectModelIntoBody(bodyBytes, deployment)
	if err != nil {
		writeError(w, 400, "invalid request body: "+err.Error())
		return
	}

	// Rewrite to the standard OpenAI gateway path. The api-version query param is
	// preserved (harmless) but ignored by the gateway.
	modifiedReq := &http.Request{
		Method:     "POST",
		URL:        &url.URL{Path: "/v1/chat/completions", RawQuery: r.URL.RawQuery},
		Header:     r.Header.Clone(),
		Body:       io.NopCloser(bytes.NewReader(newBody)),
		RemoteAddr: r.RemoteAddr,
		Host:       r.Host,
	}
	modifiedReq.ContentLength = int64(len(newBody))

	// Response is already OpenAI-format, so route directly (no response translation).
	handleGatewayRequest(w, modifiedReq)
}

// injectModelIntoBody sets/overrides the "model" field in a JSON request body.
func injectModelIntoBody(body []byte, model string) ([]byte, error) {
	var m map[string]json.RawMessage
	if len(body) > 0 {
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, err
		}
	}
	if m == nil {
		m = map[string]json.RawMessage{}
	}
	// Prefer the path deployment as the authoritative model (Azure contract).
	modelBytes, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	m["model"] = json.RawMessage(modelBytes)
	return json.Marshal(m)
}
