package main

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestExtractZaiAPIKey(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "snake_case", raw: `{"type":"glm-quota","api_key":"abc123"}`, want: "abc123"},
		{name: "kebab-case", raw: `{"type":"glm-quota","api-key":"abc123"}`, want: "abc123"},
		{name: "camelCase", raw: `{"type":"glm-quota","apiKey":"abc123"}`, want: "abc123"},
		{name: "whitespace trimmed", raw: `{"api_key":"  abc123  "}`, want: "abc123"},
		{name: "missing key", raw: `{"type":"glm-quota"}`, wantErr: true},
		{name: "empty key", raw: `{"api_key":""}`, wantErr: true},
		{name: "empty file", raw: ``, wantErr: true},
		{name: "not json", raw: `not json`, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractZaiAPIKey([]byte(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("extractZaiAPIKey(%q) = %q, nil; want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractZaiAPIKey(%q) returned error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("extractZaiAPIKey(%q) = %q; want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestQuotaBucketFromLimit(t *testing.T) {
	t.Run("computes remaining fraction from usage, not number", func(t *testing.T) {
		// number/unit describe the window's PERIOD ("5 hours"), not its
		// credit allowance -- usage is the allowance. A regression here
		// (using number as the denominator) previously produced fractions
		// like 6000/12000=... no, worse: remaining/number, which only
		// looked right by coincidence on fully-fresh or fully-spent data.
		resetAt := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
		limit := zaiQuotaWireLimit{
			Type:          "CREDIT_LIMIT",
			Unit:          json.Number("3"),
			Number:        json.Number("5"),
			Usage:         json.Number("2000"),
			Remaining:     json.Number("500"),
			NextResetTime: json.Number(mustMillis(resetAt)),
		}
		bucket, ok := quotaBucketFromLimit(limit)
		if !ok {
			t.Fatalf("quotaBucketFromLimit(%+v) reported not ok", limit)
		}
		if bucket.Window != "5h" {
			t.Errorf("Window = %q; want %q", bucket.Window, "5h")
		}
		if got, want := bucket.RemainingFraction, 0.25; got != want {
			t.Errorf("RemainingFraction = %v; want %v (500/2000, not 500/5)", got, want)
		}
		if bucket.ResetTime != resetAt.Format(time.RFC3339) {
			t.Errorf("ResetTime = %q; want %q", bucket.ResetTime, resetAt.Format(time.RFC3339))
		}
	})

	t.Run("weekly window label", func(t *testing.T) {
		limit := zaiQuotaWireLimit{Type: "CREDIT_LIMIT", Unit: json.Number("6"), Number: json.Number("1"), Usage: json.Number("12000"), Remaining: json.Number("12000")}
		bucket, ok := quotaBucketFromLimit(limit)
		if !ok {
			t.Fatalf("quotaBucketFromLimit(%+v) reported not ok", limit)
		}
		if bucket.Window != "weekly" {
			t.Errorf("Window = %q; want %q", bucket.Window, "weekly")
		}
		if bucket.RemainingFraction != 1 {
			t.Errorf("RemainingFraction = %v; want 1", bucket.RemainingFraction)
		}
	})

	t.Run("5h and weekly windows with identical type are distinguishable", func(t *testing.T) {
		// Confirmed live: z.ai sends "type":"CREDIT_LIMIT" on every entry --
		// identical across windows. unit+number is the only signal that
		// actually tells them apart.
		five := zaiQuotaWireLimit{Type: "CREDIT_LIMIT", Unit: json.Number("3"), Number: json.Number("5"), Usage: json.Number("10"), Remaining: json.Number("10")}
		week := zaiQuotaWireLimit{Type: "CREDIT_LIMIT", Unit: json.Number("6"), Number: json.Number("1"), Usage: json.Number("10"), Remaining: json.Number("10")}
		fiveBucket, _ := quotaBucketFromLimit(five)
		weekBucket, _ := quotaBucketFromLimit(week)
		if fiveBucket.Window == weekBucket.Window {
			t.Fatalf("both windows labeled %q; two different periods must not render identically", fiveBucket.Window)
		}
	})

	t.Run("unrecognized unit still gets a distinct, non-empty label", func(t *testing.T) {
		limit := zaiQuotaWireLimit{Type: "CREDIT_LIMIT", Unit: json.Number("99"), Number: json.Number("10"), Usage: json.Number("10"), Remaining: json.Number("5")}
		bucket, ok := quotaBucketFromLimit(limit)
		if !ok {
			t.Fatalf("quotaBucketFromLimit(%+v) reported not ok", limit)
		}
		if bucket.Window == "" || bucket.Window == "CREDIT_LIMIT" {
			t.Errorf("Window = %q; want a label that still distinguishes an unknown unit, not a bare repeat of type", bucket.Window)
		}
	})

	t.Run("zero usage is rejected, not misread as 0%% used", func(t *testing.T) {
		limit := zaiQuotaWireLimit{Type: "CREDIT_LIMIT", Usage: json.Number("0"), Remaining: json.Number("0")}
		if _, ok := quotaBucketFromLimit(limit); ok {
			t.Fatalf("quotaBucketFromLimit(%+v) should reject zero usage, not report 0/0 as usable", limit)
		}
	})

	t.Run("no reset time scheduled is left empty, not zero-epoch", func(t *testing.T) {
		limit := zaiQuotaWireLimit{Type: "CREDIT_LIMIT", Unit: json.Number("3"), Number: json.Number("5"), Usage: json.Number("10"), Remaining: json.Number("10")}
		bucket, ok := quotaBucketFromLimit(limit)
		if !ok {
			t.Fatalf("quotaBucketFromLimit(%+v) reported not ok", limit)
		}
		if bucket.ResetTime != "" {
			t.Errorf("ResetTime = %q; want empty when nextResetTime is absent", bucket.ResetTime)
		}
	})
}

func TestToQuotaFetchResponse(t *testing.T) {
	t.Run("maps plan, level and multiple windows", func(t *testing.T) {
		wire := zaiQuotaWireResponse{
			Data: zaiQuotaWireData{
				Level:    "pro",
				PlanName: "GLM Coding Plan Pro",
				Limits: []zaiQuotaWireLimit{
					{Type: "CREDIT_LIMIT", Unit: json.Number("3"), Number: json.Number("5"), Usage: json.Number("12000"), Remaining: json.Number("6000")},
					{Type: "CREDIT_LIMIT", Unit: json.Number("6"), Number: json.Number("1"), Usage: json.Number("84000"), Remaining: json.Number("84000")},
				},
			},
		}
		resp := toQuotaFetchResponse(wire)
		if resp.Subscription == nil || resp.Subscription.Plan != "GLM Coding Plan Pro" || resp.Subscription.TierName != "pro" {
			t.Fatalf("Subscription = %+v; want plan %q tier %q", resp.Subscription, "GLM Coding Plan Pro", "pro")
		}
		if len(resp.Groups) != 1 || len(resp.Groups[0].Buckets) != 2 {
			t.Fatalf("Groups = %+v; want one group with two buckets", resp.Groups)
		}
	})

	t.Run("no usable limits yields no groups, not a fabricated empty bucket", func(t *testing.T) {
		wire := zaiQuotaWireResponse{Data: zaiQuotaWireData{
			Limits: []zaiQuotaWireLimit{{Type: "CREDIT_LIMIT", Usage: json.Number("0"), Remaining: json.Number("0")}},
		}}
		resp := toQuotaFetchResponse(wire)
		if len(resp.Groups) != 0 {
			t.Fatalf("Groups = %+v; want none when every limit is unusable", resp.Groups)
		}
	})

	t.Run("live production shape (anonymized)", func(t *testing.T) {
		// Captured from a real quota.limit response via the deployed
		// plugin, 2026-10-20; only the account-identifying fields (none
		// were present -- this endpoint returns no account identifiers)
		// are as observed.
		const raw = `{
			"code": 200,
			"msg": "Operation successful",
			"data": {
				"limits": [
					{"type":"CREDIT_LIMIT","unit":3,"number":5,"usage":28000,"currentValue":0,"remaining":28000,"percentage":0},
					{"type":"CREDIT_LIMIT","unit":6,"number":1,"usage":140000,"currentValue":140092,"remaining":0,"percentage":100,"nextResetTime":1792750961999}
				],
				"level": "max"
			},
			"success": true
		}`
		var wire zaiQuotaWireResponse
		if err := json.Unmarshal([]byte(raw), &wire); err != nil {
			t.Fatalf("decode: %v", err)
		}
		resp := toQuotaFetchResponse(wire)
		if resp.Subscription == nil || resp.Subscription.TierName != "max" {
			t.Fatalf("Subscription = %+v; want tier %q", resp.Subscription, "max")
		}
		if len(resp.Groups) != 1 || len(resp.Groups[0].Buckets) != 2 {
			t.Fatalf("Groups = %+v; want one group with two buckets", resp.Groups)
		}
		buckets := resp.Groups[0].Buckets
		byWindow := map[string]float64{buckets[0].Window: buckets[0].RemainingFraction, buckets[1].Window: buckets[1].RemainingFraction}
		if frac, ok := byWindow["5h"]; !ok || frac != 1 {
			t.Errorf("5h bucket = %v (present=%v); want RemainingFraction 1 (28000/28000, fully available)", frac, ok)
		}
		if frac, ok := byWindow["weekly"]; !ok || frac != 0 {
			t.Errorf("weekly bucket = %v (present=%v); want RemainingFraction 0 (0/140000, fully spent)", frac, ok)
		}
	})
}

func mustMillis(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}
