package main

import "github.com/iruanp/aiwr/internal/core"

func makeTextAuditReport(report core.FileReport, path string) map[string]any {
	flagged := []any{}
	textReport := report.Text
	densityTier := "uncalibrated"
	var stylometry map[string]any
	if textReport != nil {
		for _, hit := range textReport.Hits {
			flagged = append(flagged, map[string]any{
				"detector": "unicode", "kind": hit.Kind, "label": hit.Label,
				"count": hit.Count, "sample_offsets": hit.SampleOffset, "severity": hit.Confidence,
			})
		}
		stylometry = textReport.Stylometry
	}
	if stylometry != nil {
		if value, ok := stylometry["density_tier"].(string); ok && value != "" {
			densityTier = value
		}
		switch markers := stylometry["matched_markers"].(type) {
		case []map[string]any:
			for _, marker := range markers {
				flagged = append(flagged, textAuditMarker(marker, densityTier))
			}
		case []any:
			for _, raw := range markers {
				if marker, ok := raw.(map[string]any); ok {
					flagged = append(flagged, textAuditMarker(marker, densityTier))
				}
			}
		}
	}
	result := map[string]any{
		"path": path, "density_tier": densityTier,
		"flagged_count": len(flagged), "flagged": flagged,
	}
	if stylometry != nil {
		result["stylometry"] = stylometry
	}
	return result
}

func textAuditMarker(marker map[string]any, severity string) map[string]any {
	return map[string]any{
		"detector": "stylometry", "phrase": marker["phrase"], "count": marker["count"],
		"weight": marker["weight"], "samples": marker["samples"], "spans": marker["spans"],
		"severity": severity,
	}
}

func textAuditSuspicious(audit map[string]any, report core.FileReport) bool {
	flagged, _ := audit["flagged"].([]any)
	return len(flagged) > 0 || report.Text != nil && core.StylometrySuspicious(report.Text.Stylometry)
}
