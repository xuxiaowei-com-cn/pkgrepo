package debrepo

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCompareVersions covers typical cases of dpkg --compare-versions, ensuring that the version
// comparison behaves like apt/dpkg.
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0", "0", 0},
		{"0", "0-0", 0},
		{"1.0", "1.0", 0},
		{"1.0-0", "1.0", 0},
		{"1.0-1", "1.0-01", 0},
		{"1.0-1", "1.0-001", 0},
		{"0:1.0", "1.0", 0},
		{"1:1.0", "1:1.0", 0},
		{"1.0", "1.0-1", -1},
		{"1.0~rc1", "1.0", -1},
		{"1.0~rc1", "1.0~rc2", -1},
		{"1.0~rc1", "1.0-1", -1},
		{"1.0~beta1", "1.0~beta2", -1},
		{"1.0~", "1.0", -1},
		{"1.0", "1.0~", 1},
		{"1.0-0ubuntu1", "1.0-1", -1},
		{"1.0-1ubuntu1", "1.0-1", 1},
		{"1.0-1", "1.0-1+b1", -1},
		{"1.0+dfsg", "1.0", 1},
		{"0.4a", "0.4", 1},
		// In dpkg numeric segments sort before alphabetic ones: "1.1" < "1.a".
		{"1.1", "1.a", -1},
		{"1.0", "1.0.1", -1},
		{"1.0.1", "1.0", 1},
		{"1.0", "1.1", -1},
		{"0.9", "1.0", -1},
		{"1.0", "2.0", -1},
		{"1:1.0", "1.0", 1},
		{"1:1.0", "2.0", 1},
		{"2:1.0", "1:2.0", 1},
		{"1.0-1", "1.0-2", -1},
		{"1.0a", "1.0", 1},
		{"1.0", "1.0a", -1},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := CompareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct {
		raw      string
		epoch    int
		upstream string
		revision string
		want     string
	}{
		{raw: "1.22.1-9", epoch: 0, upstream: "1.22.1", revision: "9", want: "1.22.1-9"},
		{raw: "1:1.22.1-9", epoch: 1, upstream: "1.22.1", revision: "9", want: "1:1.22.1-9"},
		{raw: "2.36", epoch: 0, upstream: "2.36", revision: "", want: "2.36"},
		{raw: "1.0-1+b1", epoch: 0, upstream: "1.0", revision: "1+b1", want: "1.0-1+b1"},
		{raw: "0:1.0", epoch: 0, upstream: "1.0", revision: "", want: "1.0"},
		{raw: "1.0-1-2", epoch: 0, upstream: "1.0-1", revision: "2", want: "1.0-1-2"},
	}
	for _, tc := range cases {
		got, err := ParseVersion(tc.raw)
		if err != nil {
			t.Errorf("ParseVersion(%q) failed: %v", tc.raw, err)
			continue
		}
		if got.Epoch != tc.epoch || got.Upstream != tc.upstream || got.Revision != tc.revision {
			t.Errorf("ParseVersion(%q) = %+v, want epoch=%d upstream=%q revision=%q",
				tc.raw, got, tc.epoch, tc.upstream, tc.revision)
		}
		if got.String() != tc.want {
			t.Errorf("ParseVersion(%q).String() = %q, want %q", tc.raw, got.String(), tc.want)
		}
	}
}

func TestParseVersionInvalid(t *testing.T) {
	for _, raw := range []string{"", "  ", ":1.0", "a:1.0", "-1", "1:"} {
		if _, err := ParseVersion(raw); err == nil {
			t.Errorf("ParseVersion(%q) expected an error", raw)
		}
	}
}

func TestVersionJSON(t *testing.T) {
	data, err := json.Marshal(MustParseVersion("1:1.22.1-9"))
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if string(data) != `"1:1.22.1-9"` {
		t.Errorf("Marshal = %s, want \"1:1.22.1-9\"", data)
	}
	var parsed Version
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed.String() != "1:1.22.1-9" {
		t.Errorf("Unmarshal = %+v", parsed)
	}
	var zero Version
	data, err = json.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("Marshal of an empty version = %s, want null", data)
	}
	if !strings.Contains(string(data), "null") {
		t.Errorf("an empty version should serialize to null, got %s", data)
	}
}

func TestVersionText(t *testing.T) {
	var parsed Version
	if err := parsed.UnmarshalText([]byte("2:1.0~rc1-3")); err != nil {
		t.Fatalf("UnmarshalText failed: %v", err)
	}
	if parsed.Epoch != 2 || parsed.Upstream != "1.0~rc1" || parsed.Revision != "3" {
		t.Errorf("UnmarshalText = %+v", parsed)
	}
	text, err := parsed.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText failed: %v", err)
	}
	if string(text) != "2:1.0~rc1-3" {
		t.Errorf("MarshalText = %q", text)
	}
}
