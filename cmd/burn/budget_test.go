package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tanrikuluozlem/burn/internal/analyzer"
	"github.com/tanrikuluozlem/burn/internal/budget"
)

func testCostReport() *analyzer.CostReport {
	return &analyzer.CostReport{
		HourlyCost:       1.0,
		MonthlyCost:      730,
		TotalMonthlyCost: 730,
		Namespaces: []analyzer.NamespaceCost{
			{Name: "app", MonthlyCost: 365},
		},
	}
}

func TestRunBudgetEvaluationNopWithoutConfig(t *testing.T) {
	res, err := runBudgetEvaluation(testCostReport(), "", nil, budgetTuning{}, false, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Error("expected nil result when neither snapshot path nor budgets are configured (legacy output)")
	}
}

func TestRunBudgetEvaluationNoPathNoFile(t *testing.T) {
	dir := t.TempDir()
	// Budgets configured but no snapshot path: evaluates in memory, writes nothing.
	budgets := []budget.Budget{{Name: "cluster", Amount: 1000, Thresholds: []float64{0.8}}}
	res, err := runBudgetEvaluation(testCostReport(), "", budgets, budgetTuning{}, false, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("expected a result for configured budgets")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("unexpected files: %v", entries)
	}
}

func TestRunBudgetEvaluationSavesAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap.json")
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	if _, err := runBudgetEvaluation(testCostReport(), path, nil, budgetTuning{}, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}
	// Reload via the file store sees the one observation.
	snap, err := budget.NewFileStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Records) != 1 || snap.Records[0].HourlyCost != 1.0 {
		t.Errorf("snapshot records wrong: %+v", snap.Records)
	}

	// A second evaluation integrates the stored observation; still near zero
	// because a single point at 12:00 leaves the whole month before it unknown.
	res, err := runBudgetEvaluation(testCostReport(), path, nil, budgetTuning{}, false, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if res.Accrual.PartialStart != true {
		t.Error("expected partial-start limitation after a mid-month first run")
	}
	if res.Accrual.KnownCost > 2 {
		t.Errorf("KnownCost = %.2f, want small; mid-month start must not integrate the early month as zero", res.Accrual.KnownCost)
	}
}

func TestRunBudgetEvaluationNoSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap.json")
	if _, err := runBudgetEvaluation(testCostReport(), path, nil, budgetTuning{}, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("--budget-no-save created a file: %v", err)
	}
}

func TestOutputBudgetJSONNil(t *testing.T) {
	if err := outputBudgetJSON(nil); err != nil {
		t.Errorf("outputBudgetJSON(nil) = %v", err)
	}
}

func TestNamespaceHourlyCosts(t *testing.T) {
	got := namespaceHourlyCosts(testCostReport())
	if got["app"] != 0.5 {
		t.Errorf("app = %v, want 0.5", got["app"])
	}
	if namespaceHourlyCosts(&analyzer.CostReport{}) != nil {
		t.Error("expected nil for empty report")
	}
}

func TestBudgetTextContainsKeyFacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snap.json")
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	res, err := runBudgetEvaluation(testCostReport(), path, []budget.Budget{
		{Name: "cluster", Amount: 1000, Thresholds: []float64{0.8}},
	}, budgetTuning{}, false, now)
	if err != nil {
		t.Fatal(err)
	}
	out := budget.FormatText(res)
	for _, want := range []string{"Month-to-date", "coverage", "forecast", "Trend", "cluster"} {
		if !strings.Contains(out, want) {
			t.Errorf("text missing %q:\n%s", want, out)
		}
	}
}
