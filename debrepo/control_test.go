package debrepo

import (
	"errors"
	"strings"
	"testing"
)

func TestParseStanzas(t *testing.T) {
	const data = `Package: nginx
Version: 1.22.1-9
Depends: libc6 (>= 2.34),
 libssl3,
 libpcre2-8-0
Description: small, powerful, scalable web/proxy server
 Nginx is a web server and a reverse proxy.
 .
 This is the second paragraph.
# 这是注释，应当被忽略
Section: httpd

Package: curl
Version: 7.88.1-10
`
	stanzas, err := ParseStanzas(strings.NewReader(data))
	if err != nil {
		t.Fatalf("ParseStanzas 失败: %v", err)
	}
	if len(stanzas) != 2 {
		t.Fatalf("解析出 %d 个段落，期望 2", len(stanzas))
	}
	first := &stanzas[0]
	if got := first.Get("package"); got != "nginx" {
		t.Errorf("Package = %q", got)
	}
	if got := first.Get("Depends"); got != "libc6 (>= 2.34),\nlibssl3,\nlibpcre2-8-0" {
		t.Errorf("Depends = %q", got)
	}
	want := "small, powerful, scalable web/proxy server\nNginx is a web server and a reverse proxy.\n.\nThis is the second paragraph."
	if got := first.Get("Description"); got != want {
		t.Errorf("Description = %q，期望 %q", got, want)
	}
	if !first.Has("Section") || first.Has("Homepage") {
		t.Errorf("Has 判断错误: %v", first.Names())
	}
	if got := first.Len(); got != 5 {
		t.Errorf("Len = %d，期望 5（Package/Version/Depends/Description/Section）", got)
	}
	if got := stanzas[1].Get("Package"); got != "curl" {
		t.Errorf("第二个段落的 Package = %q", got)
	}
}

func TestParseStanzasFuncStreaming(t *testing.T) {
	const data = "Package: a\n\nPackage: b\n\n\nPackage: c\n"
	var names []string
	err := ParseStanzasFunc(strings.NewReader(data), func(stanza *Stanza) error {
		names = append(names, stanza.Get("Package"))
		return nil
	})
	if err != nil {
		t.Fatalf("ParseStanzasFunc 失败: %v", err)
	}
	if strings.Join(names, ",") != "a,b,c" {
		t.Errorf("解析结果 = %v", names)
	}
}

func TestParseStanzasError(t *testing.T) {
	cases := []string{
		" Package: nginx\n",     // 续行出现在字段之前
		"Package nginx\n",       // 缺少冒号
		"Package: nginx\nBad\n", // 非法行
	}
	for _, data := range cases {
		err := ParseStanzasFunc(strings.NewReader(data), func(*Stanza) error { return nil })
		if err == nil {
			t.Errorf("输入 %q 期望返回错误", data)
			continue
		}
		if !errors.Is(err, ErrInvalidControl) {
			t.Errorf("输入 %q 的错误应为 ErrInvalidControl，实际 %v", data, err)
		}
	}
}

func TestStanzaString(t *testing.T) {
	stanza := &Stanza{values: map[string]string{}}
	stanza.set("Package", "nginx")
	stanza.set("Depends", "libc6 (>= 2.34)")
	stanza.appendLine("Depends", "libssl3")
	stanza.set("Description", "web server")
	stanza.set("Homepage", "")
	want := "Package: nginx\nDepends: libc6 (>= 2.34)\n libssl3\nDescription: web server\nHomepage:\n"
	if got := stanza.String(); got != want {
		t.Errorf("String() = %q，期望 %q", got, want)
	}
	fields := stanza.Fields()
	if len(fields) != 4 || fields["Homepage"] != "" {
		t.Errorf("Fields() = %v", fields)
	}
	// 同名字段（不区分大小写）应当覆盖而不是重复。
	stanza.set("PACKAGE", "curl")
	if got := stanza.Get("package"); got != "curl" {
		t.Errorf("重复字段覆盖后 = %q", got)
	}
	if got := stanza.Len(); got != 4 {
		t.Errorf("重复字段覆盖后 Len = %d，期望 4", got)
	}
}
