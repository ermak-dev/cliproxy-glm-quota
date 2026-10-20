package main

// Host contract types, inlined rather than imported.
//
// CLIProxyAPI's plugin SDK (sdk/pluginapi, sdk/pluginabi) is pure Go with no
// cgo of its own, but pulling the whole router-for-me/CLIProxyAPI module as a
// dependency for a handful of structs and four string constants is more
// weight than this plugin needs. These types are copied field-for-field from
// sdk/pluginapi/types.go and sdk/pluginabi/types.go as of the pinned host
// version below; a host upgrade that changes this wire shape needs a matching
// update here.
//
// Pinned against: github.com/router-for-me/CLIProxyAPI v8.0.16, plugin schema
// version 6 (sdk/pluginabi.SchemaVersion), ABI version 1.

import (
	"encoding/json"
	"time"
)

const (
	abiVersion    = 1
	schemaVersion = 6

	methodPluginRegister    = "plugin.register"
	methodPluginReconfigure = "plugin.reconfigure"

	methodAuthIdentifier = "auth.identifier"
	methodAuthParse      = "auth.parse"
	methodAuthLoginStart = "auth.login.start"
	methodAuthLoginPoll  = "auth.login.poll"
	methodAuthRefresh    = "auth.refresh"

	methodQuotaIdentifier = "quota.identifier"
	methodQuotaDescribe   = "quota.describe"
	methodQuotaFetch      = "quota.fetch"
	methodQuotaReset      = "quota.reset"

	methodManagementRegister = "management.register"
	methodManagementHandle   = "management.handle"

	// methodHostAuthSave is called the other way: this plugin calls INTO the
	// host (via callHost in main.go), not the reverse.
	methodHostAuthSave = "host.auth.save"
)

// envelope mirrors sdk/pluginabi.Envelope: every plugin.call response, success
// or failure, is wrapped in this shape.
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

// pluginMetadata mirrors sdk/pluginapi.Metadata. No json tags on the real
// type, so field names marshal verbatim (capitalized) -- matches the
// official examples/plugin/auth/go example byte for byte.
type pluginMetadata struct {
	Name             string
	Version          string
	Author           string
	GitHubRepository string
	Logo             string
	ConfigFields     []any
}

// registrationCapabilities mirrors the host's rpcCapabilities wire shape
// (internal/pluginhost/rpc_client.go). Every field must be present: the host
// decodes this with a fixed struct, and an omitted bool defaults to false,
// which is exactly what every capability but auth_provider/quota_provider
// needs.
type registrationCapabilities struct {
	ModelRegistrar                bool   `json:"model_registrar"`
	ModelProvider                 bool   `json:"model_provider"`
	AuthProvider                  bool   `json:"auth_provider"`
	FrontendAuthProvider          bool   `json:"frontend_auth_provider"`
	FrontendAuthProviderExclusive bool   `json:"frontend_auth_provider_exclusive"`
	Scheduler                     bool   `json:"scheduler"`
	ModelRouter                   bool   `json:"model_router"`
	Executor                      bool   `json:"executor"`
	ExecutorModelScope            string `json:"executor_model_scope"`
	RequestTranslator             bool   `json:"request_translator"`
	RequestNormalizer             bool   `json:"request_normalizer"`
	RequestInterceptor            bool   `json:"request_interceptor"`
	RequestLifecyclePlugin        bool   `json:"request_lifecycle_plugin"`
	ResponseTranslator            bool   `json:"response_translator"`
	ResponseBeforeTranslator      bool   `json:"response_before_translator"`
	ResponseAfterTranslator       bool   `json:"response_after_translator"`
	ResponseInterceptor           bool   `json:"response_interceptor"`
	StreamChunkInterceptor        bool   `json:"response_stream_interceptor"`
	WebSocketResponseObserver     bool   `json:"websocket_response_observer"`
	ThinkingApplier               bool   `json:"thinking_applier"`
	UsagePlugin                   bool   `json:"usage_plugin"`
	CommandLinePlugin             bool   `json:"command_line_plugin"`
	ManagementAPI                 bool   `json:"management_api"`
	QuotaProvider                 bool   `json:"quota_provider"`
}

// resourceRoute mirrors sdk/pluginapi.ResourceRoute (no json tags). Resource
// requests are explicitly documented as not management-authenticated AND,
// confirmed empirically against a running v8.0.16 host, GET-only: a POST to
// a registered resource path 404s before the plugin is ever called. So this
// plugin only ever registers a GET resource, to render the form; see
// managementRoute for where the form actually submits to.
type resourceRoute struct {
	Path        string
	Menu        string
	Description string
}

// managementRoute mirrors sdk/pluginapi.ManagementRoute (no json tags).
// Unlike ResourceRoute, the host's management middleware authenticates
// these with the Bearer management key before this plugin is ever called --
// which is exactly why the save action lives here and not on the resource
// page: it is the only registered-route kind that both accepts POST and
// lets the host (not this plugin, which has no way to check the key itself)
// verify the caller is allowed to write a credential.
type managementRoute struct {
	Method string
	Path   string
}

// managementRegistrationResponse is the host's rpcManagementRegistrationResponse
// wire wrapper (internal/pluginhost/rpc_client.go) -- note this outer struct
// IS tagged (lowercase), unlike ManagementRoute/ResourceRoute themselves.
type managementRegistrationResponse struct {
	Routes    []managementRoute `json:"routes,omitempty"`
	Resources []resourceRoute   `json:"resources,omitempty"`
}

// managementRequest mirrors sdk/pluginapi.ManagementRequest (no json tags).
type managementRequest struct {
	Method string
	Path   string
	Query  map[string][]string
	Body   []byte
}

// managementResponse mirrors sdk/pluginapi.ManagementResponse (no json tags).
type managementResponse struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

// hostAuthSaveRequest mirrors sdk/pluginapi.HostAuthSaveRequest -- tagged,
// lowercase, unlike the untagged types above.
type hostAuthSaveRequest struct {
	Name string          `json:"name"`
	JSON json.RawMessage `json:"json"`
}

type registrationResponse struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginMetadata           `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

// authData mirrors sdk/pluginapi.AuthData. No json tags on the real type.
type authData struct {
	Provider         string
	ID               string
	FileName         string
	Label            string
	Prefix           string
	ProxyURL         string
	Disabled         bool
	StorageJSON      []byte
	Metadata         map[string]any
	Attributes       map[string]string
	NextRefreshAfter time.Time
}

// authParseRequest mirrors sdk/pluginapi.AuthParseRequest (no json tags).
type authParseRequest struct {
	Provider string
	Path     string
	FileName string
	RawJSON  []byte
	Host     any
}

// authParseResponse mirrors sdk/pluginapi.AuthParseResponse (no json tags).
type authParseResponse struct {
	Handled bool
	Auth    authData
	Auths   []authData
}

// quotaDescribeResponse mirrors sdk/pluginapi.QuotaDescribeResponse.
type quotaDescribeResponse struct {
	SupportedProviders []string `json:"supported_providers,omitempty"`
	DisplayName        string   `json:"display_name,omitempty"`
	SupportsReset      bool     `json:"supports_reset,omitempty"`
}

// quotaFetchRequest mirrors sdk/pluginapi.QuotaFetchRequest (plus the
// host_callback_id wrapper rpcQuotaFetchRequest adds on the wire). Only the
// fields this plugin reads are decoded; everything else is ignored.
type quotaFetchRequest struct {
	AuthIndex   string            `json:"auth_index"`
	AuthID      string            `json:"auth_id"`
	Provider    string            `json:"provider"`
	StorageJSON []byte            `json:"storage_json,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
}

// quotaSubscription mirrors sdk/pluginapi.QuotaSubscription.
type quotaSubscription struct {
	Plan     string `json:"plan,omitempty"`
	TierName string `json:"tierName,omitempty"`
	TierID   string `json:"tierId,omitempty"`
}

// quotaBucket mirrors sdk/pluginapi.QuotaBucket.
type quotaBucket struct {
	Window            string  `json:"window,omitempty"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
	Description       string  `json:"description,omitempty"`
}

// quotaGroup mirrors sdk/pluginapi.QuotaGroup.
type quotaGroup struct {
	DisplayName string        `json:"displayName,omitempty"`
	Buckets     []quotaBucket `json:"buckets,omitempty"`
}

// quotaFetchResponse mirrors sdk/pluginapi.QuotaFetchResponse.
type quotaFetchResponse struct {
	Subscription *quotaSubscription `json:"subscription,omitempty"`
	Groups       []quotaGroup       `json:"groups,omitempty"`
}
