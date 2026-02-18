package run

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"echohelix/internal/ledger"
)

func (s *Service) SetDailyTokenQuota(quotas map[string]int64) {
	next := make(map[string]int64, len(quotas))
	for k, v := range quotas {
		name := strings.TrimSpace(k)
		if name == "" || v <= 0 {
			continue
		}
		next[name] = v
	}
	s.mu.Lock()
	s.dailyTokenQuota = next
	s.mu.Unlock()
}

func (s *Service) TokenUsage(ctx context.Context, from, to time.Time, backend string) (TokenUsageSummary, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return TokenUsageSummary{}, fmt.Errorf("invalid time range")
	}
	aggs, err := s.ledger.AggregateTokenUsage(ctx, from, to, backend)
	if err != nil {
		return TokenUsageSummary{}, err
	}
	out := TokenUsageSummary{
		From:      from.UTC(),
		To:        to.UTC(),
		ByBackend: make([]TokenUsageByBackend, 0, len(aggs)),
	}
	for _, agg := range aggs {
		item := TokenUsageByBackend{
			Backend: agg.Backend,
			TokenUsageTotals: TokenUsageTotals{
				RunCount:     agg.RunCount,
				InputTokens:  agg.InputTokens,
				OutputTokens: agg.OutputTokens,
				TotalTokens:  agg.TotalTokens,
			},
		}
		out.ByBackend = append(out.ByBackend, item)
		out.Totals.RunCount += agg.RunCount
		out.Totals.InputTokens += agg.InputTokens
		out.Totals.OutputTokens += agg.OutputTokens
		out.Totals.TotalTokens += agg.TotalTokens
	}
	return out, nil
}

func (s *Service) TokenQuota(ctx context.Context, now time.Time, backend string) ([]TokenQuotaItem, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	windowFrom := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	windowTo := now.Add(time.Nanosecond)

	summary, err := s.TokenUsage(ctx, windowFrom, windowTo, backend)
	if err != nil {
		return nil, err
	}
	usageByBackend := make(map[string]int64, len(summary.ByBackend))
	for _, item := range summary.ByBackend {
		usageByBackend[item.Backend] = item.TotalTokens
	}

	quota := s.snapshotDailyTokenQuota()
	backendSet := map[string]struct{}{}
	if b := strings.TrimSpace(backend); b != "" {
		backendSet[b] = struct{}{}
	} else {
		for _, d := range s.registry.All() {
			backendSet[d.Name()] = struct{}{}
		}
		for name := range quota {
			backendSet[name] = struct{}{}
		}
		for name := range usageByBackend {
			backendSet[name] = struct{}{}
		}
	}

	names := make([]string, 0, len(backendSet))
	for name := range backendSet {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]TokenQuotaItem, 0, len(names))
	for _, name := range names {
		used := usageByBackend[name]
		limit, configured := quota[name]
		item := TokenQuotaItem{
			Backend:    name,
			WindowFrom: windowFrom,
			WindowTo:   now,
			Configured: configured,
			UsedTokens: used,
		}
		if configured {
			item.QuotaTokens = limit
			item.RemainingTokens = limit - used
			if item.RemainingTokens < 0 {
				item.Exceeded = true
			}
		} else {
			item.RemainingTokens = -1
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) snapshotDailyTokenQuota() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.dailyTokenQuota))
	for k, v := range s.dailyTokenQuota {
		out[k] = v
	}
	return out
}

func (s *Service) recordTokenUsage(ctx context.Context, runID, backend string, payload map[string]any) {
	usage, ok := parseTokenUsage(payload)
	if !ok {
		return
	}
	_ = s.ledger.UpsertTokenUsage(ctx, ledger.TokenUsageRecord{
		RunID:        runID,
		Backend:      backend,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.TotalTokens,
		RecordedAt:   time.Now().UTC(),
	})
}

func parseTokenUsage(payload map[string]any) (TokenUsageTotals, bool) {
	usage := mapPayload(payload, "usage")
	if usage == nil {
		usage = mapPayload(payload, "stats")
	}
	if usage == nil {
		usage = payload
	}
	if usage == nil {
		return TokenUsageTotals{}, false
	}

	input := pickTokenValue(usage,
		"input_tokens", "prompt_tokens", "inputTokenCount", "promptTokenCount", "inputTokens", "promptTokens",
	)
	output := pickTokenValue(usage,
		"output_tokens", "completion_tokens", "outputTokenCount", "candidatesTokenCount", "outputTokens", "completionTokens",
	)
	total := pickTokenValue(usage,
		"total_tokens", "totalTokenCount", "totalTokens", "total",
	)
	if total == 0 && (input > 0 || output > 0) {
		total = input + output
	}
	if input == 0 && output == 0 && total == 0 {
		return TokenUsageTotals{}, false
	}
	return TokenUsageTotals{
		RunCount:     1,
		InputTokens:  input,
		OutputTokens: output,
		TotalTokens:  total,
	}, true
}

func mapPayload(payload map[string]any, key string) map[string]any {
	if payload == nil {
		return nil
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return nil
	}
	obj, _ := v.(map[string]any)
	return obj
}

func pickTokenValue(m map[string]any, keys ...string) int64 {
	for _, key := range keys {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		if n, ok := int64FromAny(v); ok && n >= 0 {
			return n
		}
	}
	return 0
}

func int64FromAny(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint:
		return int64(n), true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		if n > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(n), true
	case float32:
		return int64(n), true
	case float64:
		return int64(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		if f, err := n.Float64(); err == nil {
			return int64(f), true
		}
		return 0, false
	case string:
		s := strings.TrimSpace(n)
		if s == "" {
			return 0, false
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}
