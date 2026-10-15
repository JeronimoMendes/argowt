package filter

import (
	"strings"
	"testing"
	"time"
)

func TestStructuredExpression(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	expression, err := Parse("status=Running namespace!=system age>10m label:team=payments pay")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	document := Document{
		Name:              "daily-payment",
		Namespace:         "argo",
		Status:            "Running",
		Labels:            map[string]string{"team": "payments"},
		CreationTimestamp: now.Add(-time.Hour),
	}
	if !expression.Match(document, now) {
		t.Fatal("expression should match document")
	}
	document.Status = "Failed"
	if expression.Match(document, now) {
		t.Fatal("expression should reject a different status")
	}
}

func TestLabelSelector(t *testing.T) {
	expression, err := Parse("label:team=payments label:environment!=test status=Running")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	selector := expression.LabelSelector()
	if !strings.Contains(selector, "team=payments") || !strings.Contains(selector, "environment!=test") {
		t.Fatalf("LabelSelector() = %q", selector)
	}
}

func TestTimePredicates(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	document := Document{CreationTimestamp: now.Add(-2 * time.Hour)}
	for _, query := range []string{"age>=2h", "created<2026-10-15T11:00:00Z", "created>=2026-10-15"} {
		expression, err := Parse(query)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", query, err)
		}
		if !expression.Match(document, now) {
			t.Fatalf("%q should match", query)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, query := range []string{
		"status=",
		"status>Running",
		"age=10m",
		"age>soon",
		"unknown=value",
		"label:=value",
	} {
		t.Run(query, func(t *testing.T) {
			if _, err := Parse(query); err == nil {
				t.Fatalf("Parse(%q) expected an error", query)
			}
		})
	}
}

func TestUnknownTimestampDoesNotMatchTimePredicate(t *testing.T) {
	expression, err := Parse("age>1m")
	if err != nil {
		t.Fatal(err)
	}
	if expression.Match(Document{}, time.Now()) {
		t.Fatal("missing creation timestamp should not match")
	}
}
