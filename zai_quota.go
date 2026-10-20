package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// zaiQuotaEndpoint is the same endpoint the z.ai web console reads from at
// https://z.ai/manage-apikey/coding-plan/personal/usage. It is not
// configurable: a fixed host+path keeps this plugin from being turned into
// an open SSRF relay through plugin config.
const zaiQuotaEndpoint = "https://api.z.ai/api/monitor/usage/quota/limit"

const maxQuotaResponseBytes = 1 << 20 // 1MiB; the real payload is a few KB.

// zaiQuotaWireResponse is the exact shape api.z.ai/api/monitor/usage/quota/limit
// returns. Field names come from observing the response, not from z.ai's own
// (nonexistent) public schema doc.
type zaiQuotaWireResponse struct {
	Code    json.Number      `json:"code"`
	Success *bool            `json:"success"`
	Message string           `json:"message,omitempty"`
	Data    zaiQuotaWireData `json:"data"`
}

type zaiQuotaWireData struct {
	Level    string              `json:"level"`
	PlanName string              `json:"planName"`
	Limits   []zaiQuotaWireLimit `json:"limits"`
}

// zaiQuotaWireLimit's field meanings were confirmed by calling the live
// endpoint, not assumed from docs (z.ai publishes none for this response):
// `type` is literally "CREDIT_LIMIT" on every observed entry -- useless for
// telling windows apart. The actual window size is `number` combined with
// `unit` (observed: unit 3 + number 5 on a window that stays unset until
// used and would reset within hours once exhausted = the 5-hour window;
// unit 6 + number 1 on a window whose observed reset was 3 days out, too
// far for an hourly cycle = the weekly window). `usage` -- not `number` --
// is the actual credit allowance for that window; an earlier version of
// this file used `number` as the allowance, which happened to produce a
// clamped-correct-looking fraction (1 or 0) only because both observed
// samples were fully-fresh or fully-spent -- any partial usage would have
// been wrong.
type zaiQuotaWireLimit struct {
	Type          string      `json:"type"`
	Unit          json.Number `json:"unit"`
	Number        json.Number `json:"number"`
	Usage         json.Number `json:"usage"`
	CurrentValue  json.Number `json:"currentValue"`
	Remaining     json.Number `json:"remaining"`
	NextResetTime json.Number `json:"nextResetTime"`
}

// extractZaiAPIKey reads the api_key field out of a GLM-quota auth file,
// tolerating the three spellings other CLIProxyAPI plugins in this family
// use (api_key / api-key / apiKey).
func extractZaiAPIKey(storageJSON []byte) (string, error) {
	if len(storageJSON) == 0 {
		return "", fmt.Errorf("auth file has no stored data")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(storageJSON, &fields); err != nil {
		return "", fmt.Errorf("auth file is not valid JSON: %w", err)
	}
	for _, name := range []string{"api_key", "api-key", "apiKey"} {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		var key string
		if err := json.Unmarshal(raw, &key); err != nil {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			return key, nil
		}
	}
	return "", fmt.Errorf("auth file has no api_key field")
}

// fetchZaiQuota calls the z.ai quota endpoint and returns the raw wire
// response. Split out from parsing so a test can exercise parsing without a
// live network call.
func fetchZaiQuota(ctx context.Context, httpClient *http.Client, apiKey string) (zaiQuotaWireResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zaiQuotaEndpoint, nil)
	if err != nil {
		return zaiQuotaWireResponse{}, fmt.Errorf("build quota request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return zaiQuotaWireResponse{}, fmt.Errorf("quota request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQuotaResponseBytes+1))
	if err != nil {
		return zaiQuotaWireResponse{}, fmt.Errorf("read quota response: %w", err)
	}
	if len(body) > maxQuotaResponseBytes {
		return zaiQuotaWireResponse{}, fmt.Errorf("quota response exceeds %d bytes", maxQuotaResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return zaiQuotaWireResponse{}, fmt.Errorf("quota endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var wire zaiQuotaWireResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return zaiQuotaWireResponse{}, fmt.Errorf("malformed quota response: %w", err)
	}
	if wire.Success != nil && !*wire.Success {
		msg := wire.Message
		if msg == "" {
			msg = "upstream marked the response unsuccessful"
		}
		return zaiQuotaWireResponse{}, fmt.Errorf("z.ai quota request rejected: %s", msg)
	}
	return wire, nil
}

// toQuotaFetchResponse maps the z.ai wire shape onto the host's normalized
// quota contract: one bucket per upstream limit entry (observed as a
// 5-hour and a weekly window, see windowLabel), generic on purpose so a
// third window upstream adds later still shows up, just with a label built
// from raw unit/number instead of a recognized name.
func toQuotaFetchResponse(wire zaiQuotaWireResponse) quotaFetchResponse {
	resp := quotaFetchResponse{}
	if plan := strings.TrimSpace(wire.Data.PlanName); plan != "" || strings.TrimSpace(wire.Data.Level) != "" {
		resp.Subscription = &quotaSubscription{
			Plan:     plan,
			TierName: strings.TrimSpace(wire.Data.Level),
		}
	}

	group := quotaGroup{DisplayName: "GLM Coding Plan"}
	for _, limit := range wire.Data.Limits {
		bucket, ok := quotaBucketFromLimit(limit)
		if !ok {
			continue
		}
		group.Buckets = append(group.Buckets, bucket)
	}
	if len(group.Buckets) > 0 {
		resp.Groups = []quotaGroup{group}
	}
	return resp
}

func quotaBucketFromLimit(limit zaiQuotaWireLimit) (quotaBucket, bool) {
	total, errTotal := limit.Usage.Float64()
	remaining, errRemaining := limit.Remaining.Float64()
	if errTotal != nil || errRemaining != nil || total <= 0 {
		return quotaBucket{}, false
	}
	fraction := remaining / total
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	bucket := quotaBucket{
		Window:            windowLabel(limit),
		RemainingFraction: fraction,
	}
	if resetMs, errReset := limit.NextResetTime.Float64(); errReset == nil && resetMs > 0 {
		bucket.ResetTime = time.UnixMilli(int64(resetMs)).UTC().Format(time.RFC3339)
	}
	return bucket, true
}

// windowLabel turns (unit, number) into the short label the quota card
// renders. `type` is always "CREDIT_LIMIT" on every observed entry, so it
// can't distinguish windows at all; see the comment on zaiQuotaWireLimit for
// how unit 3/6 were identified. An unrecognized unit still gets a label
// distinct per window (not a second identical "CREDIT_LIMIT") rather than
// being silently merged with every other window of unknown meaning.
func windowLabel(limit zaiQuotaWireLimit) string {
	number, errNumber := limit.Number.Float64()
	unit, errUnit := limit.Unit.Float64()
	if errNumber == nil && errUnit == nil {
		switch unit {
		case 3:
			return fmt.Sprintf("%gh", number)
		case 6:
			if number == 1 {
				return "weekly"
			}
			return fmt.Sprintf("%g weeks", number)
		}
	}
	label := strings.TrimSpace(limit.Type)
	if label == "" {
		label = "window"
	}
	if errNumber == nil && errUnit == nil {
		label = fmt.Sprintf("%s (unit %g ×%g)", label, unit, number)
	}
	return label
}
