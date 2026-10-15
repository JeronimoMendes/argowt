package filter

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		fields []string
		want   bool
	}{
		{name: "empty", query: "", fields: []string{"anything"}, want: true},
		{name: "case insensitive", query: "PAY", fields: []string{"daily-payment"}, want: true},
		{name: "fuzzy subsequence", query: "dpy", fields: []string{"daily-payment"}, want: true},
		{name: "tokens across fields", query: "argo run", fields: []string{"argo", "Running"}, want: true},
		{name: "missing token", query: "argo failed", fields: []string{"argo", "Running"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Match(test.query, test.fields...); got != test.want {
				t.Fatalf("Match(%q, %v) = %t, want %t", test.query, test.fields, got, test.want)
			}
		})
	}
}
