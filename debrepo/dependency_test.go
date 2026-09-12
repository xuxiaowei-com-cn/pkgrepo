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
			t.Errorf("ParseDependencies(%q) 失败: %v", tc.field, err)
			continue
		}
		if got := deps.String(); got != tc.want {
			t.Errorf("ParseDependencies(%q).String() = %q，期望 %q", tc.field, got, tc.want)
		}
	}
}

func TestParseDependenciesEmpty(t *testing.T) {
	deps, err := ParseDependencies("  ")
	if err != nil || deps != nil {
		t.Errorf("空依赖字段应当返回 nil, nil，实际 %v, %v", deps, err)
	}
	if got := deps.String(); got != "" {
		t.Errorf("空依赖 String() = %q", got)
	}
	if deps.Has("anything") {
		t.Error("空依赖不应包含任何包")
	}
}

func TestParseDependenciesFields(t *testing.T) {
	deps, err := ParseDependencies("libc6 (>= 2.34) | libc6.1, python3:any <!nocheck>")
	if err != nil {
		t.Fatalf("ParseDependencies 失败: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("依赖数量 = %d，期望 2", len(deps))
	}
	first := deps[0]
	if len(first.Alternatives) != 2 {
		t.Fatalf("替代方案数量 = %d，期望 2", len(first.Alternatives))
	}
	if first.Alternatives[0].Name != "libc6" || first.Alternatives[0].Operator != ">=" ||
		first.Alternatives[0].Version != "2.34" || !first.Alternatives[0].IsVersioned() {
		t.Errorf("第一个约束 = %+v", first.Alternatives[0])
	}
	if first.Alternatives[1].Name != "libc6.1" || first.Alternatives[1].IsVersioned() {
		t.Errorf("第二个约束 = %+v", first.Alternatives[1])
	}
	second := deps[1]
	if second.Alternatives[0].Arch != "any" || second.Alternatives[0].Name != "python3" {
		t.Errorf("多架构限定 = %+v", second.Alternatives[0])
	}
	if len(second.Alternatives[0].Profiles) != 1 || second.Alternatives[0].Profiles[0] != "!nocheck" {
		t.Errorf("profile 限制 = %+v", second.Alternatives[0])
	}
	if !deps.Has("libc6.1") || !deps.Has("python3") || deps.Has("libssl3") {
		t.Errorf("Has 判断错误: %v", deps.Names())
	}
	if dep, ok := deps.Find("python3"); !ok || dep.Alternatives[0].Arch != "any" {
		t.Errorf("Find(python3) = %+v, %t", dep, ok)
	}
}

func TestParseDependenciesErrors(t *testing.T) {
	cases := []string{
		"libc6 (>=",        // 缺少右括号
		"libc6 >= 1.0",     // 缺少括号
		"libc6 (1.0)",      // 缺少关系运算符
		"libc6 (>= )",      // 缺少版本号
		"libbar [amd64",    // 缺少右方括号
		"libbaz <!nocheck", // 缺少右尖括号
		"|",                // 空的替代方案
	}
	for _, field := range cases {
		if _, err := ParseDependencies(field); err == nil {
			t.Errorf("ParseDependencies(%q) 期望返回错误", field)
		} else if !errors.Is(err, ErrInvalidRelation) {
			t.Errorf("ParseDependencies(%q) 的错误应为 ErrInvalidRelation，实际 %v", field, err)
		}
	}
}

func TestDependenciesJSON(t *testing.T) {
	deps, err := ParseDependencies("libc6 (>= 2.34), libssl3")
	if err != nil {
		t.Fatalf("ParseDependencies 失败: %v", err)
	}
	data, err := json.Marshal(deps)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	var items []string
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("Marshal 结果不是字符串数组: %s（%v）", data, err)
	}
	if len(items) != 2 || !strings.HasPrefix(items[0], "libc6 (>= 2.34)") || items[1] != "libssl3" {
		t.Errorf("Marshal = %s", data)
	}
	var decoded Dependencies
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal 失败: %v", err)
	}
	if decoded.String() != deps.String() {
		t.Errorf("Unmarshal = %q，期望 %q", decoded.String(), deps.String())
	}
	var nilDeps Dependencies
	data, err = json.Marshal(nilDeps)
	if err != nil {
		t.Fatalf("Marshal 失败: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("nil 依赖 Marshal = %s，期望 null", data)
	}
}
