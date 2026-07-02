package toolchain

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type Status string

const (
	StatusPresent Status = "present"
	StatusMissing Status = "missing"
	StatusUnknown Status = "unknown"
)

type Result struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
}

type Detector interface {
	Detect(context.Context, string) (Status, error)
}

type FakeDetector struct {
	Statuses map[string]Status
}

type UnknownDetector struct{}

func Check(ctx context.Context, requirements []string, detector Detector) ([]Result, error) {
	names := uniqueSorted(requirements)
	if len(names) == 0 {
		return nil, nil
	}
	if detector == nil {
		detector = UnknownDetector{}
	}
	results := make([]Result, 0, len(names))
	for _, name := range names {
		status, err := detector.Detect(ctx, name)
		if err != nil {
			return nil, err
		}
		if !validStatus(status) {
			return nil, fmt.Errorf("toolchain requirement %s returned unsupported status %q", name, status)
		}
		results = append(results, Result{Name: name, Status: status})
	}
	return results, nil
}

func (f FakeDetector) Detect(_ context.Context, name string) (Status, error) {
	if f.Statuses == nil {
		return StatusUnknown, nil
	}
	status, ok := f.Statuses[strings.TrimSpace(name)]
	if !ok {
		return StatusUnknown, nil
	}
	return status, nil
}

func (UnknownDetector) Detect(context.Context, string) (Status, error) {
	return StatusUnknown, nil
}

func validStatus(status Status) bool {
	switch status {
	case StatusPresent, StatusMissing, StatusUnknown:
		return true
	default:
		return false
	}
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
