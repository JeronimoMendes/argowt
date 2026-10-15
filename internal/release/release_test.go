package release

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		current, tag string
		want         bool
	}{
		{"v1.2.3", "v1.2.4", true},
		{"v1.2.3", "v1.10.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v2.0.0", "v1.9.9", false},
		{"v1.2.3-rc.1", "v1.2.3", true},
		{"main", "v1.0.0", true},
		{"v1.0.0", "garbage", false},
	}
	for _, testCase := range cases {
		if got := Newer(testCase.current, testCase.tag); got != testCase.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", testCase.current, testCase.tag, got, testCase.want)
		}
	}
}

func TestChecksum(t *testing.T) {
	sums := []byte("aaa  argowt-linux-arm64\nbbb  argowt-linux-amd64\n")

	if got, err := checksum(sums, "argowt-linux-amd64"); err != nil || got != "bbb" {
		t.Fatalf("checksum() = %q, %v", got, err)
	}
	if _, err := checksum(sums, "argowt-darwin-arm64"); err == nil {
		t.Fatal("checksum() found missing asset")
	}
}
