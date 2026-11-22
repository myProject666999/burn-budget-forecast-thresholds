package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tanrikuluozlem/burn/internal/analyzer"
)

func testReport() *analyzer.CostReport {
	return &analyzer.CostReport{
		HourlyCost:       1.0,
		MonthlyCost:      730,
		TotalMonthlyCost: 730,
		Namespaces: []analyzer.NamespaceCost{
			{Name: "app", MonthlyCost: 365},
			{Name: "infra", MonthlyCost: 365},
		},
	}
}

func TestEvaluateBudgetNoSnapshotCreatesNoFile(t *testing.T) {
	dir := t.TempDir()
	s := &Server{config: Config{}} // no snapshot path, no budget file
	res, err := s.evaluateBudget(testReport())
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("expected a result even without a snapshot path")
	}
	// No snapshot path: nothing should be written anywhere under the temp dir.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("unexpected files created: %v", entries)
	}
	if res.DataQuality.Sufficient {
		t.Error("no-history evaluation should be insufficient, not fabricate month-to-date")
	}
}

func TestEvaluateBudgetWritesSnapshotWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "budget.json")
	s := &Server{config: Config{SnapshotPath: path}}

	res, err := s.evaluateBudget(testReport())
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	// Namespace hourly detail is recorded from the report.
	if res.Record().Namespaces["app"] == 0 {
		t.Errorf("expected namespace detail on the observation, got %+v", res.Record().Namespaces)
	}
}

func TestReportNamespaceHourly(t *testing.T) {
	got := reportNamespaceHourly(testReport())
	if got["app"] != 0.5 {
		t.Errorf("app hourly = %v, want 0.5", got["app"])
	}
	if reportNamespaceHourly(&analyzer.CostReport{}) != nil {
		t.Error("expected nil for empty namespaces")
	}
}
