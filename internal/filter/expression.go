package filter

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/labels"
)

type Document struct {
	Name              string
	Namespace         string
	Status            string
	Template          string
	InstanceID        string
	Labels            map[string]string
	CreationTimestamp time.Time
}

type Expression struct {
	fuzzy      []string
	predicates []predicate
	selector   string
}

type predicate struct {
	field    string
	operator string
	value    string
	duration time.Duration
	time     time.Time
}

func Parse(query string) (Expression, error) {
	expression := Expression{}
	var labelClauses []string
	for _, token := range strings.Fields(query) {
		field, operator, value, structured := splitPredicate(token)
		if !structured {
			expression.fuzzy = append(expression.fuzzy, token)
			continue
		}
		if strings.HasPrefix(strings.ToLower(field), "label:") {
			field = "label:" + field[len("label:"):]
		} else {
			field = strings.ToLower(field)
		}
		if value == "" {
			return Expression{}, fmt.Errorf("%s requires a value", field)
		}
		parsed := predicate{field: field, operator: operator, value: value}
		switch {
		case field == "status" || field == "phase" || field == "namespace" || field == "name" || field == "template" || field == "instanceid":
			if operator != "=" && operator != "!=" {
				return Expression{}, fmt.Errorf("%s supports only = and !=", field)
			}
		case field == "age":
			if operator == "=" || operator == "!=" {
				return Expression{}, fmt.Errorf("age requires <, <=, >, or >=")
			}
			duration, err := time.ParseDuration(value)
			if err != nil || duration < 0 {
				return Expression{}, fmt.Errorf("invalid age %q", value)
			}
			parsed.duration = duration
		case field == "created":
			if operator == "=" || operator == "!=" {
				return Expression{}, fmt.Errorf("created requires <, <=, >, or >=")
			}
			created, err := parseTime(value)
			if err != nil {
				return Expression{}, fmt.Errorf("invalid created time %q", value)
			}
			parsed.time = created
		case strings.HasPrefix(field, "label:"):
			key := strings.TrimPrefix(field, "label:")
			if key == "" || (operator != "=" && operator != "!=") {
				return Expression{}, fmt.Errorf("label predicates require label:key=value or label:key!=value")
			}
			labelClauses = append(labelClauses, key+operator+value)
		default:
			return Expression{}, fmt.Errorf("unknown filter field %q", field)
		}
		expression.predicates = append(expression.predicates, parsed)
	}
	if len(labelClauses) > 0 {
		selector := strings.Join(labelClauses, ",")
		parsed, err := labels.Parse(selector)
		if err != nil {
			return Expression{}, fmt.Errorf("invalid label selector: %w", err)
		}
		expression.selector = parsed.String()
	}
	return expression, nil
}

func (expression Expression) Match(document Document, now time.Time) bool {
	fields := []string{document.Name, document.Namespace, document.Status, document.Template, document.InstanceID}
	for _, token := range expression.fuzzy {
		if !Match(token, fields...) {
			return false
		}
	}
	for _, predicate := range expression.predicates {
		if !predicate.match(document, now) {
			return false
		}
	}
	return true
}

func (expression Expression) LabelSelector() string {
	return expression.selector
}

func (expression Expression) Empty() bool {
	return len(expression.fuzzy) == 0 && len(expression.predicates) == 0
}

func (predicate predicate) match(document Document, now time.Time) bool {
	var actual string
	switch predicate.field {
	case "status", "phase":
		actual = document.Status
	case "namespace":
		actual = document.Namespace
	case "name":
		actual = document.Name
	case "template":
		actual = document.Template
	case "instanceid":
		actual = document.InstanceID
	case "age":
		if document.CreationTimestamp.IsZero() {
			return false
		}
		return compareDuration(now.Sub(document.CreationTimestamp), predicate.duration, predicate.operator)
	case "created":
		if document.CreationTimestamp.IsZero() {
			return false
		}
		return compareTime(document.CreationTimestamp, predicate.time, predicate.operator)
	default:
		if strings.HasPrefix(predicate.field, "label:") {
			actual = document.Labels[strings.TrimPrefix(predicate.field, "label:")]
		}
	}
	return compareString(actual, predicate.value, predicate.operator)
}

func splitPredicate(token string) (string, string, string, bool) {
	operators := []string{"!=", ">=", "<=", "=", ">", "<"}
	for _, operator := range operators {
		if index := strings.Index(token, operator); index > 0 {
			return token[:index], operator, token[index+len(operator):], true
		}
	}
	return "", "", "", false
}

func parseTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	return time.Parse(time.DateOnly, value)
}

func compareString(actual, expected, operator string) bool {
	equal := strings.EqualFold(actual, expected)
	if operator == "!=" {
		return !equal
	}
	return equal
}

func compareDuration(actual, expected time.Duration, operator string) bool {
	switch operator {
	case ">":
		return actual > expected
	case ">=":
		return actual >= expected
	case "<":
		return actual < expected
	case "<=":
		return actual <= expected
	default:
		return false
	}
}

func compareTime(actual, expected time.Time, operator string) bool {
	switch operator {
	case ">":
		return actual.After(expected)
	case ">=":
		return actual.Equal(expected) || actual.After(expected)
	case "<":
		return actual.Before(expected)
	case "<=":
		return actual.Equal(expected) || actual.Before(expected)
	default:
		return false
	}
}

// Fields returns the supported structured filter names for contextual help.
func Fields() []string {
	fields := []string{"age", "created", "instanceid", "label:<key>", "name", "namespace", "status", "template"}
	sort.Strings(fields)
	return fields
}
