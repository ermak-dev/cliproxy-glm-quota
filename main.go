// Command glm-quota is a native CLIProxyAPI plugin (built as a c-shared
// library) that surfaces Z.ai GLM Coding Plan quota -- the same numbers as
// https://z.ai/manage-apikey/coding-plan/personal/usage -- as a quota card
// in CLIProxyAPI's built-in Management Center.
//
// It does exactly two things and nothing else: parses a small auth-dir JSON
// file holding a GLM Coding Plan API key (AuthProvider), and answers the
// host's quota.fetch calls for that credential by querying z.ai directly
// (QuotaProvider). It is not a scheduler and not an executor: it never
// touches how GLM inference traffic is routed, and it never has to agree
// with whatever provider name the GLM inference credential already uses.
//
// Install: either open the "GLM Coding Plan" page under the Management
// Center's Plugins nav (served by this plugin's own ManagementAPI resource,
// see management.go) and paste the API key there, or drop a file named
// glm-quota.json straight into the proxy's auth-dir:
//
//	{"type": "glm-quota", "api_key": "<your coding-plan key>", "label": "GLM Coding Plan"}
//
// The host's existing GLM inference credential is untouched; this is a
// second, monitoring-only credential pointed at the same key.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

// This plugin calls back into the host exactly once -- host.auth.save, from
// the add-credential form -- so the call/free_buffer function pointers given
// to cliproxy_plugin_init are stashed in a single static, matching the
// official examples/plugin/host-callback-auth-files pattern.
static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unsafe"
)

const pluginIdentifier = "glm-quota"

// httpClient is shared across quota.fetch calls. 15s matches the kind of
// timeout the comparable zai-coding-plan plugin uses for the same endpoint;
// a plugin call blocks the host's calling goroutine, so this must not hang.
var httpClient = &http.Client{Timeout: 15 * time.Second}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(abiVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}

	var rawRequest []byte
	if request != nil && requestLen > 0 {
		rawRequest = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}

	raw, errHandle := handleMethod(C.GoString(method), rawRequest)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	_ = length
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case methodPluginRegister, methodPluginReconfigure:
		return okEnvelope(registrationResponse{
			SchemaVersion: schemaVersion,
			Metadata: pluginMetadata{
				Name:             "glm-quota",
				Version:          "0.1.0",
				Author:           "ermak.dev",
				GitHubRepository: "https://github.com/ermak-dev/cliproxy-glm-quota",
			},
			Capabilities: registrationCapabilities{
				AuthProvider:  true,
				QuotaProvider: true,
				ManagementAPI: true,
			},
		})

	case methodAuthIdentifier, methodQuotaIdentifier:
		return okEnvelope(identifierResponse{Identifier: pluginIdentifier})

	case methodAuthParse:
		return handleAuthParse(request)

	case methodAuthLoginStart, methodAuthLoginPoll, methodAuthRefresh:
		// This plugin only loads a static API key already placed in
		// auth-dir; there is no interactive login flow and the key never
		// expires on a schedule the host needs to drive.
		return nil, fmt.Errorf("method not supported: %s is a static API-key credential", method)

	case methodQuotaDescribe:
		return okEnvelope(quotaDescribeResponse{
			SupportedProviders: []string{pluginIdentifier},
			DisplayName:        "GLM Coding Plan (Z.ai)",
			SupportsReset:      false,
		})

	case methodQuotaFetch:
		return handleQuotaFetch(request)

	case methodQuotaReset:
		return nil, fmt.Errorf("method not supported: quota reset is not offered by z.ai's usage endpoint")

	case methodManagementRegister:
		return handleManagementRegister()

	case methodManagementHandle:
		return handleManagementHandle(request)

	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

func handleAuthParse(request []byte) ([]byte, error) {
	var req authParseRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, fmt.Errorf("decode auth.parse request: %w", err)
	}

	if _, errKey := extractZaiAPIKey(req.RawJSON); errKey != nil {
		return nil, fmt.Errorf("glm-quota auth file: %w", errKey)
	}

	label := "GLM Coding Plan"
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(req.RawJSON, &fields); err == nil {
		if raw, ok := fields["label"]; ok {
			var custom string
			if err := json.Unmarshal(raw, &custom); err == nil && strings.TrimSpace(custom) != "" {
				label = strings.TrimSpace(custom)
			}
		}
	}

	fileName := req.FileName
	if fileName == "" {
		fileName = "glm-quota.json"
	}

	return okEnvelope(authParseResponse{
		Handled: true,
		Auth: authData{
			Provider:    pluginIdentifier,
			ID:          pluginIdentifier,
			FileName:    fileName,
			Label:       label,
			StorageJSON: req.RawJSON,
			Metadata:    map[string]any{"type": pluginIdentifier},
		},
	})
}

func handleQuotaFetch(request []byte) ([]byte, error) {
	var req quotaFetchRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, fmt.Errorf("decode quota.fetch request: %w", err)
	}

	apiKey, errKey := extractZaiAPIKey(req.StorageJSON)
	if errKey != nil {
		return nil, fmt.Errorf("credential %s: %w", req.AuthIndex, errKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	wire, errFetch := fetchZaiQuota(ctx, httpClient, apiKey)
	if errFetch != nil {
		return nil, errFetch
	}

	return okEnvelope(toQuotaFetchResponse(wire))
}

func okEnvelope(result any) ([]byte, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return json.Marshal(envelope{OK: true, Result: resultJSON})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

// callHost makes a synchronous call into the host, the mirror image of
// cliproxyPluginCall. Used exactly once, by the add-credential form, to
// invoke host.auth.save.
func callHost(method string, payload []byte) (json.RawMessage, error) {
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var reqPtr *C.uint8_t
	if len(payload) > 0 {
		reqPtr = (*C.uint8_t)(C.CBytes(payload))
		defer C.free(unsafe.Pointer(reqPtr))
	}

	var resp C.cliproxy_buffer
	if rc := C.call_host_api(cMethod, reqPtr, C.size_t(len(payload)), &resp); rc != 0 {
		return nil, fmt.Errorf("host call %s failed (no host callback registered)", method)
	}
	if resp.ptr == nil || resp.len == 0 {
		return nil, fmt.Errorf("host call %s returned an empty response", method)
	}
	raw := C.GoBytes(resp.ptr, C.int(resp.len))
	C.free_host_buffer(resp.ptr, resp.len)

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode host response for %s: %w", method, err)
	}
	if !env.OK {
		msg := "host rejected the call"
		if env.Error != nil && env.Error.Message != "" {
			msg = env.Error.Message
		}
		return nil, fmt.Errorf("%s: %s", method, msg)
	}
	return env.Result, nil
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
