package rpmrepo

import "testing"

// TestCompareVersions uses every case from the upstream rpm tests/rpmvercmp.at, ensuring that the
// version comparison behaves exactly like rpm/dnf.
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.0", "2.0", -1},
		{"2.0", "1.0", 1},
		{"2.0.1", "2.0.1", 0},
		{"2.0", "2.0.1", -1},
		{"2.0.1", "2.0", 1},
		{"2.0.1a", "2.0.1a", 0},
		{"2.0.1a", "2.0.1", 1},
		{"2.0.1", "2.0.1a", -1},
		{"5.5p1", "5.5p1", 0},
		{"5.5p1", "5.5p2", -1},
		{"5.5p2", "5.5p1", 1},
		{"5.5p10", "5.5p10", 0},
		{"5.5p1", "5.5p10", -1},
		{"5.5p10", "5.5p1", 1},
		{"10xyz", "10.1xyz", -1},
		{"10.1xyz", "10xyz", 1},
		{"xyz10", "xyz10", 0},
		{"xyz10", "xyz10.1", -1},
		{"xyz10.1", "xyz10", 1},
		{"xyz.4", "xyz.4", 0},
		{"xyz.4", "8", -1},
		{"8", "xyz.4", 1},
		{"xyz.4", "2", -1},
		{"2", "xyz.4", 1},
		{"5.5p2", "5.6p1", -1},
		{"5.6p1", "5.5p2", 1},
		{"5.6p1", "6.5p1", -1},
		{"6.5p1", "5.6p1", 1},
		{"6.0.rc1", "6.0", 1},
		{"6.0", "6.0.rc1", -1},
		{"10b2", "10a1", 1},
		{"10a2", "10b2", -1},
		{"1.0aa", "1.0aa", 0},
		{"1.0a", "1.0aa", -1},
		{"1.0aa", "1.0a", 1},
		{"10.0001", "10.0001", 0},
		{"10.0001", "10.1", 0},
		{"10.1", "10.0001", 0},
		{"10.0001", "10.0039", -1},
		{"10.0039", "10.0001", 1},
		{"4.999.9", "5.0", -1},
		{"5.0", "4.999.9", 1},
		{"20101121", "20101121", 0},
		{"20101121", "20101122", -1},
		{"20101122", "20101121", 1},
		{"2_0", "2_0", 0},
		{"2.0", "2_0", 0},
		{"2_0", "2.0", 0},
		{"a", "a", 0},
		{"a+", "a+", 0},
		{"a+", "a_", 0},
		{"a_", "a+", 0},
		{"+a", "+a", 0},
		{"+a", "_a", 0},
		{"_a", "+a", 0},
		{"+_", "+_", 0},
		{"_+", "+_", 0},
		{"_+", "_+", 0},
		{"+", "_", 0},
		{"_", "+", 0},
		{"1.0~rc1", "1.0~rc1", 0},
		{"1.0~rc1", "1.0", -1},
		{"1.0", "1.0~rc1", 1},
		{"1.0~rc1", "1.0~rc2", -1},
		{"1.0~rc2", "1.0~rc1", 1},
		{"1.0~rc1~git123", "1.0~rc1~git123", 0},
		{"1.0~rc1~git123", "1.0~rc1", -1},
		{"1.0~rc1", "1.0~rc1~git123", 1},
		{"1.0^", "1.0^", 0},
		{"1.0^", "1.0", 1},
		{"1.0", "1.0^", -1},
		{"1.0^git1", "1.0^git1", 0},
		{"1.0^git1", "1.0", 1},
		{"1.0", "1.0^git1", -1},
		{"1.0^git1", "1.0^git2", -1},
		{"1.0^git2", "1.0^git1", 1},
		{"1.0^git1", "1.01", -1},
		{"1.01", "1.0^git1", 1},
		{"1.0^20160101", "1.0^20160101", 0},
		{"1.0^20160101", "1.0.1", -1},
		{"1.0.1", "1.0^20160101", 1},
		{"1.0^20160101^git1", "1.0^20160101^git1", 0},
		{"1.0^20160102", "1.0^20160101^git1", 1},
		{"1.0^20160101^git1", "1.0^20160102", -1},
		{"1.0~rc1^git1", "1.0~rc1^git1", 0},
		{"1.0~rc1^git1", "1.0~rc1", 1},
		{"1.0~rc1", "1.0~rc1^git1", -1},
		{"1.0^git1~pre", "1.0^git1~pre", 0},
		{"1.0^git1", "1.0^git1~pre", 1},
		{"1.0^git1~pre", "1.0^git1", -1},
	}

	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		// Swapping the arguments must negate the result, which guarantees the comparison is
		// antisymmetric.
		if got := CompareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
}

func TestEVRString(t *testing.T) {
	cases := []struct {
		evr  EVR
		want string
	}{
		{EVR{Epoch: "3", Version: "24.0.7", Release: "1.el7"}, "3:24.0.7-1.el7"},
		{EVR{Epoch: "0", Version: "24.0.7", Release: "1.el7"}, "24.0.7-1.el7"},
		{EVR{Version: "24.0.7"}, "24.0.7"},
	}
	for _, tc := range cases {
		if got := tc.evr.String(); got != tc.want {
			t.Errorf("EVR.String() = %q, want %q", got, tc.want)
		}
	}
}

func TestEVRCompare(t *testing.T) {
	cases := []struct {
		a, b EVR
		want int
	}{
		{EVR{Epoch: "1", Version: "1.0", Release: "1"}, EVR{Epoch: "0", Version: "9.0", Release: "9"}, 1},
		{EVR{Version: "1.0", Release: "2"}, EVR{Version: "1.0", Release: "10"}, -1},
		{EVR{Version: "1.0", Release: "rc1"}, EVR{Version: "1.0", Release: "1"}, -1},
	}
	for _, tc := range cases {
		if got := tc.a.Compare(tc.b); got != tc.want {
			t.Errorf("%v.Compare(%v) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestDependencyString(t *testing.T) {
	cases := []struct {
		dep  Dependency
		want string
	}{
		{Dependency{Name: "libc.so.6()(64bit)"}, "libc.so.6()(64bit)"},
		{Dependency{Name: "systemd", Flags: "GE", Epoch: "0", Version: "219"}, "systemd >= 219"},
		{Dependency{Name: "docker", Flags: "LT", Epoch: "2", Version: "1.12.0", Release: "0"}, "docker < 2:1.12.0-0"},
	}
	for _, tc := range cases {
		if got := tc.dep.String(); got != tc.want {
			t.Errorf("Dependency.String() = %q, want %q", got, tc.want)
		}
	}
	if !(Dependency{Name: "x", Flags: "EQ", Version: "1"}).IsVersioned() {
		t.Error("a versioned dependency should be reported as IsVersioned")
	}
	if (Dependency{Name: "x"}).IsVersioned() {
		t.Error("a dependency without a version should not be reported as IsVersioned")
	}
}
