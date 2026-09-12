package debrepo

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseDependencies(t *testing.T) {
	cases := []struct {
		field string
		want  string
	}{
		{"libc6 (>= 2.34)", "libc6 (>= 2.34)"},
		{"libc6 (>= 2.34), libssl3", "libc6 (>= 2.34), libssl3"},
		{"libc6 (>= 2.34) | libc6.1", "libc6 (>= 2.34) | libc6.1"},
		{"debhelper-compat (= 13), libsdl2-dev", "debhelper-compat (= 13), libsdl2-dev"},
		{"default-mta | mail-transport-agent", "default-mta | mail-transport-agent"},
		{"python3:any (>= 3.11)", "python3:any (>= 3.11)"},
		{"libfoo (<< 2.0~rc1)", "libfoo (<< 2.0~rc1)"},
		{"libbar [amd64 !i386]", "libbar [amd64 !i386]"},
		{"libbaz <!nocheck>", "libbaz <!nocheck>"},
		{"libqux (>= 1.0) [linux-any] <cross>", "libqux (>= 1.0) [linux-any] <cross>"},
		{"a,\nb", "a, b"},
	}
	for _, tc := range cases {
		deps, err := ParseDependencies(tc.field)
		if err != nil {
			t.Errorf("ParseDependencies(%q) failed: %v", tc.field, err)
			continue
		}
		if got := deps.String(); got != tc.want {
			t.Errorf("ParseDependencies(%q).String() = %q, want %q", tc.field, got, tc.want)
		}
	}
}

func TestParseDependenciesEmpty(t *testing.T) {
	deps, err := ParseDependencies("  ")
	if err != nil || deps != nil {
		t.Errorf("an empty dependency field should return nil, nil, got %v, %v", deps, err)
	}
	if got := deps.String(); got != "" {
		t.Errorf("String() of empty dependencies = %q", got)
	}
	if deps.Has("anything") {
		t.Error("empty dependencies should contain no package")
	}
}

func TestParseDependenciesFields(t *testing.T) {
	deps, err := ParseDependencies("libc6 (>= 2.34) | libc6.1, python3:any <!nocheck>")
	if err != nil {
		t.Fatalf("ParseDependencies failed: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("dependency count = %d, want 2", len(deps))
	}
	first := deps[0]
	if len(first.Alternatives) != 2 {
		t.Fatalf("alternative count = %d, want 2", len(first.Alternatives))
	}
	if first.Alternatives[0].Name != "libc6" || first.Alternatives[0].Operator != ">=" ||
		first.Alternatives[0].Version != "2.34" || !first.Alternatives[0].IsVersioned() {
		t.Errorf("first constraint = %+v", first.Alternatives[0])
	}
	if first.Alternatives[1].Name != "libc6.1" || first.Alternatives[1].IsVersioned() {
		t.Errorf("second constraint = %+v", first.Alternatives[1])
	}
	second := deps[1]
	if second.Alternatives[0].Arch != "any" || second.Alternatives[0].Name != "python3" {
		t.Errorf("multi-arch qualifier = %+v", second.Alternatives[0])
	}
	if len(second.Alternatives[0].Profiles) != 1 || second.Alternatives[0].Profiles[0] != "!nocheck" {
		t.Errorf("profile restriction = %+v", second.Alternatives[0])
	}
	if !deps.Has("libc6.1") || !deps.Has("python3") || deps.Has("libssl3") {
		t.Errorf("Has returned a wrong result: %v", deps.Names())
	}
	if dep, ok := deps.Find("python3"); !ok || dep.Alternatives[0].Arch != "any" {
		t.Errorf("Find(python3) = %+v, %t", dep, ok)
	}
}

func TestParseDependenciesErrors(t *testing.T) {
	cases := []string{
		"libc6 (>=",        // missing closing parenthesis
		"libc6 >= 1.0",     // missing parentheses
		"libc6 (1.0)",      // missing relation operator
		"libc6 (>= )",      // missing version number
		"libbar [amd64",    // missing closing square bracket
		"libbaz <!nocheck", // missing closing angle bracket
		"|",                // empty alternative
	}
	for _, field := range cases {
		if _, err := ParseDependencies(field); err == nil {
			t.Errorf("ParseDependencies(%q) expected an error", field)
		} else if !errors.Is(err, ErrInvalidRelation) {
			t.Errorf("the error of ParseDependencies(%q) should be ErrInvalidRelation, got %v", field, err)
		}
	}
}

func TestDependenciesJSON(t *testing.T) {
	deps, err := ParseDependencies("libc6 (>= 2.34), libssl3")
	if err != nil {
		t.Fatalf("ParseDependencies failed: %v", err)
	}
	data, err := json.Marshal(deps)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var items []string
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("the Marshal result is not an array of strings: %s (%v)", data, err)
	}
	if len(items) != 2 || !strings.HasPrefix(items[0], "libc6 (>= 2.34)") || items[1] != "libssl3" {
		t.Errorf("Marshal = %s", data)
	}
	var decoded Dependencies
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.String() != deps.String() {
		t.Errorf("Unmarshal = %q, want %q", decoded.String(), deps.String())
	}
	var nilDeps Dependencies
	data, err = json.Marshal(nilDeps)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("Marshal of nil dependencies = %s, want null", data)
	}
}
