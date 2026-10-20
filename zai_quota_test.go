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
	t.Run("computes remaining fraction and reset time", func(t *testing.T) {
		resetAt := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
		limit := zaiQuotaWireLimit{
			Type:          "CREDIT_LIMIT_5H",
			Number:        json.Number("2000"),
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
			t.Errorf("RemainingFraction = %v; want %v", got, want)
		}
		if bucket.ResetTime != resetAt.Format(time.RFC3339) {
			t.Errorf("ResetTime = %q; want %q", bucket.ResetTime, resetAt.Format(time.RFC3339))
		}
	})

	t.Run("weekly window label", func(t *testing.T) {
		limit := zaiQuotaWireLimit{Type: "CREDIT_LIMIT_7D", Number: json.Number("12000"), Remaining: json.Number("12000")}
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

	t.Run("unrecognized type passes through", func(t *testing.T) {
		limit := zaiQuotaWireLimit{Type: "SOMETHING_NEW", Number: json.Number("10"), Remaining: json.Number("5")}
		bucket, ok := quotaBucketFromLimit(limit)
		if !ok {
			t.Fatalf("quotaBucketFromLimit(%+v) reported not ok", limit)
		}
		if bucket.Window != "SOMETHING_NEW" {
			t.Errorf("Window = %q; want pass-through %q", bucket.Window, "SOMETHING_NEW")
		}
	})

	t.Run("zero total is rejected, not misread as 0%% used", func(t *testing.T) {
		limit := zaiQuotaWireLimit{Type: "CREDIT_LIMIT_5H", Number: json.Number("0"), Remaining: json.Number("0")}
		if _, ok := quotaBucketFromLimit(limit); ok {
			t.Fatalf("quotaBucketFromLimit(%+v) should reject a zero total, not report 0/0 as usable", limit)
		}
	})

	t.Run("no reset time scheduled is left empty, not zero-epoch", func(t *testing.T) {
		limit := zaiQuotaWireLimit{Type: "CREDIT_LIMIT_5H", Number: json.Number("10"), Remaining: json.Number("10")}
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
					{Type: "CREDIT_LIMIT_5H", Number: json.Number("12000"), Remaining: json.Number("6000")},
					{Type: "CREDIT_LIMIT_7D", Number: json.Number("84000"), Remaining: json.Number("84000")},
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
			Limits: []zaiQuotaWireLimit{{Type: "CREDIT_LIMIT_5H", Number: json.Number("0"), Remaining: json.Number("0")}},
		}}
		resp := toQuotaFetchResponse(wire)
		if len(resp.Groups) != 0 {
			t.Fatalf("Groups = %+v; want none when every limit is unusable", resp.Groups)
		}
	})
}

func mustMillis(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}
