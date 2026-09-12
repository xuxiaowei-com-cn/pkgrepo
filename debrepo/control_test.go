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
# this is a comment and should be ignored
Section: httpd

Package: curl
Version: 7.88.1-10
`
	stanzas, err := ParseStanzas(strings.NewReader(data))
	if err != nil {
		t.Fatalf("ParseStanzas failed: %v", err)
	}
	if len(stanzas) != 2 {
		t.Fatalf("parsed %d paragraphs, want 2", len(stanzas))
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
		t.Errorf("Description = %q, want %q", got, want)
	}
	if !first.Has("Section") || first.Has("Homepage") {
		t.Errorf("Has returned a wrong result: %v", first.Names())
	}
	if got := first.Len(); got != 5 {
		t.Errorf("Len = %d, want 5 (Package/Version/Depends/Description/Section)", got)
	}
	if got := stanzas[1].Get("Package"); got != "curl" {
		t.Errorf("Package of the second paragraph = %q", got)
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
		t.Fatalf("ParseStanzasFunc failed: %v", err)
	}
	if strings.Join(names, ",") != "a,b,c" {
		t.Errorf("parse result = %v", names)
	}
}

func TestParseStanzasError(t *testing.T) {
	cases := []string{
		" Package: nginx\n",     // continuation line before any field
		"Package nginx\n",       // missing colon
		"Package: nginx\nBad\n", // invalid line
	}
	for _, data := range cases {
		err := ParseStanzasFunc(strings.NewReader(data), func(*Stanza) error { return nil })
		if err == nil {
			t.Errorf("input %q expected an error", data)
			continue
		}
		if !errors.Is(err, ErrInvalidControl) {
			t.Errorf("the error of input %q should be ErrInvalidControl, got %v", data, err)
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
		t.Errorf("String() = %q, want %q", got, want)
	}
	fields := stanza.Fields()
	if len(fields) != 4 || fields["Homepage"] != "" {
		t.Errorf("Fields() = %v", fields)
	}
	// A field with the same name (case-insensitively) should be overwritten, not duplicated.
	stanza.set("PACKAGE", "curl")
	if got := stanza.Get("package"); got != "curl" {
		t.Errorf("after the duplicate field was overwritten = %q", got)
	}
	if got := stanza.Len(); got != 4 {
		t.Errorf("after the duplicate field was overwritten Len = %d, want 4", got)
	}
}
