package main

import (
	"os/exec"
	"testing"
)

func clearDoctorEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"AIWR_PYTHON",
		"WATERMARKS_PYTHON",
		"AIWR_UPSTREAM_SCRIPTS",
		"WATERMARKS_UPSTREAM_SCRIPTS",
		"WATERMARKS_UPSTREAM_REPO",
		"WATERMARKS_SYNTHID_SCORER_URL",
		"WATERMARKS_SYNTHID_TEXT_URL",
		"MARKLLM_DIR",
		"NOAI_WATERMARK_DIR",
		"MARKDIFFUSION_DIR",
		"WATERMARKS_GUMBEL_KEY",
		"WATERMARKS_REWRITE_BACKEND",
		"WATERMARKS_REWRITE_MODEL",
		"WATERMARKS_REWRITE_BASE_URL",
		"WATERMARKS_REWRITE_API_KEY",
		"OPENAI_API_KEY",
		"WATERMARKS_REWRITE_ALLOW_REMOTE",
	} {
		t.Setenv(name, "")
	}
}

func doctorCheckByID(report doctorReport, id string) (doctorCheck, bool) {
	for _, check := range report.Checks {
		if check.ID == id {
			return check, true
		}
	}
	return doctorCheck{}, false
}

func TestCollectDoctorReportKeepsNativeCoreReady(t *testing.T) {
	clearDoctorEnvironment(t)

	report := collectDoctorReport()
	coreCheck, ok := doctorCheckByID(report, "core.native")
	if !ok {
		t.Fatal("doctor report does not contain core.native")
	}
	if coreCheck.Status != "ok" {
		t.Fatalf("core.native status = %q, want ok", coreCheck.Status)
	}
	if !report.Ready {
		t.Fatal("doctor report Ready = false, want true")
	}
	if report.Summary.Total != len(report.Checks) {
		t.Fatalf("summary total = %d, want %d", report.Summary.Total, len(report.Checks))
	}
	layerB, ok := doctorCheckByID(report, "backend.layerb")
	if !ok {
		t.Fatal("doctor report does not contain backend.layerb")
	}
	if layerB.Status != "missing" {
		t.Fatalf("unconfigured Layer B status = %q, want missing", layerB.Status)
	}
	if !report.OK {
		t.Fatal("unconfigured optional backends should not make report OK false")
	}
}

func TestCollectDoctorReportFlagsInvalidLayerBConfiguration(t *testing.T) {
	clearDoctorEnvironment(t)
	t.Setenv("WATERMARKS_REWRITE_BACKEND", "not-a-backend")

	report := collectDoctorReport()
	layerB, ok := doctorCheckByID(report, "backend.layerb")
	if !ok {
		t.Fatal("doctor report does not contain backend.layerb")
	}
	if layerB.Status != "error" {
		t.Fatalf("invalid Layer B status = %q, want error", layerB.Status)
	}
	if report.OK {
		t.Fatal("invalid configured backend should make report OK false")
	}
}

func TestDoctorCommandCheckOmitsInstallAdviceWhenAvailable(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go is not available on PATH")
	}

	check := doctorCommandCheck(doctorCommandSpec{
		ID:        "test.go",
		Name:      "Go",
		Program:   "go",
		Args:      []string{"version"},
		Purpose:   "doctor test",
		Configure: "Install Go.",
	})
	if check.Status != "ok" {
		t.Fatalf("Go probe status = %q, want ok", check.Status)
	}
	if len(check.Configure) != 0 {
		t.Fatalf("successful probe Configure = %#v, want no install advice", check.Configure)
	}
}
