package toolchain

import (
	"context"
	"strings"
	"testing"
)

func TestFakeDetectorReportsPresentMissingAndUnknown(t *testing.T) {
	results, err := Check(context.Background(), []string{"go", "node", "mise"}, FakeDetector{
		Statuses: map[string]Status{
			"go":   StatusPresent,
			"node": StatusMissing,
		},
	})
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}

	if got := statusByName(results, "go"); got != StatusPresent {
		t.Fatalf("go status = %q, want %q", got, StatusPresent)
	}
	if got := statusByName(results, "node"); got != StatusMissing {
		t.Fatalf("node status = %q, want %q", got, StatusMissing)
	}
	if got := statusByName(results, "mise"); got != StatusUnknown {
		t.Fatalf("mise status = %q, want %q", got, StatusUnknown)
	}
}

func TestCheckNormalizesRequirementNames(t *testing.T) {
	results, err := Check(context.Background(), []string{" go ", "", "go"}, FakeDetector{
		Statuses: map[string]Status{"go": StatusPresent},
	})
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}

	if len(results) != 1 || results[0].Name != "go" || results[0].Status != StatusPresent {
		t.Fatalf("results = %#v, want one present go requirement", results)
	}
}

func TestFakeDetectorRejectsUnsupportedStatus(t *testing.T) {
	_, err := Check(context.Background(), []string{"go"}, FakeDetector{
		Statuses: map[string]Status{"go": "installed"},
	})
	if err == nil {
		t.Fatal("Check error = nil, want unsupported status error")
	}
	if !strings.Contains(err.Error(), "go") || !strings.Contains(err.Error(), "installed") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

func statusByName(results []Result, name string) Status {
	for _, result := range results {
		if result.Name == name {
			return result.Status
		}
	}
	return ""
}
