package debrepo

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCompareVersions 覆盖 dpkg --compare-versions 的典型用例，
// 保证与 apt/dpkg 的版本比较行为一致。
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
		// dpkg 中数字段排在字母段之前："1.1" < "1.a"。
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
			t.Errorf("CompareVersions(%q, %q) = %d，期望 %d", tc.a, tc.b, got, tc.want)
		}
		if got := CompareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d，期望 %d", tc.b, tc.a, got, -tc.want)
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
			t.Errorf("ParseVersion(%q) 失败: %v", tc.raw, err)
			continue
		}
		if got.Epoch != tc.epoch || got.Upstream != tc.upstream || got.Revision != tc.revision {
			t.Errorf("ParseVersion(%q) = %+v，期望 epoch=%d upstream=%q revision=%q",
				tc.raw, got, tc.epoch, tc.upstream, tc.revision)
		}
		if got.String() != tc.want {
			t.Errorf("ParseVersion(%q).String() = %q，期望 %q", tc.raw, got.String(), tc.want)
		}
	}
}

func TestParseVersionInvalid(t *testing.T) {
	for _, raw := range []string{"", "  ", ":1.0", "a:1.0", "-1", "1:"} {
		if _, err := ParseVersion(raw); err == nil {
			t.Errorf("ParseVersion(%q) 期望返回错误", raw)
		}
	}
}

func TestVersionJSON(t *testing.T) {
	data, err := json.Marshal(MustParseVersion("1:1.22.1-9"))
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	if string(data) != `"1:1.22.1-9"` {
		t.Errorf("Marshal = %s，期望 \"1:1.22.1-9\"", data)
	}
	var parsed Version
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}
	if parsed.String() != "1:1.22.1-9" {
		t.Errorf("Unmarshal = %+v", parsed)
	}
	var zero Version
	data, err = json.Marshal(zero)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("空版本的 Marshal = %s，期望 null", data)
	}
	if !strings.Contains(string(data), "null") {
		t.Errorf("空版本应当序列化为 null，实际 %s", data)
	}
}

func TestVersionText(t *testing.T) {
	var parsed Version
	if err := parsed.UnmarshalText([]byte("2:1.0~rc1-3")); err != nil {
		t.Fatalf("UnmarshalText 失败: %v", err)
	}
	if parsed.Epoch != 2 || parsed.Upstream != "1.0~rc1" || parsed.Revision != "3" {
		t.Errorf("UnmarshalText = %+v", parsed)
	}
	text, err := parsed.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText 失败: %v", err)
	}
	if string(text) != "2:1.0~rc1-3" {
		t.Errorf("MarshalText = %q", text)
	}
}
