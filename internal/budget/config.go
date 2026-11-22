package budget

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ParseBudgets accepts a JSON array of Budget objects. A missing/empty input
// yields no budgets. Unknown fields are rejected so typos do not silently
// disable a budget.
func ParseBudgets(jsonText string) ([]Budget, error) {
	jsonText = strings.TrimSpace(jsonText)
	if jsonText == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(jsonText))
	dec.DisallowUnknownFields()
	var raw []Budget
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse budgets: %w", err)
	}
	for _, b := range raw {
		if err := ValidateBudget(b); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// LoadBudgets reads a JSON budgets file. A missing path returns no budgets.
func LoadBudgets(path string) ([]Budget, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("budgets file %s not found", path)
		}
		return nil, err
	}
	return ParseBudgets(string(data))
}
