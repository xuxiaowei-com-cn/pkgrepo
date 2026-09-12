package rpmrepo

import (
	"net/url"
	"testing"
)

func TestNormalizeRepoURL(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "https://download.docker.com/linux/centos/7/x86_64/stable", want: "https://download.docker.com/linux/centos/7/x86_64/stable/"},
		{in: "https://a.example.com/repo/", want: "https://a.example.com/repo/"},
		{in: "https://a.example.com/repo/repodata/repomd.xml", want: "https://a.example.com/repo/"},
		{in: "file:///tmp/repo", want: "file:///tmp/repo/"},
		{in: "", wantErr: true},
		{in: "ftp://a.example.com/repo", wantErr: true},
	}
	for _, tc := range cases {
		got, err := normalizeRepoURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizeRepoURL(%q) expected an error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeRepoURL(%q) failed: %v", tc.in, err)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("normalizeRepoURL(%q) = %q, want %q", tc.in, got.String(), tc.want)
		}
	}

	// A local directory is converted into a file address.
	got, err := normalizeRepoURL("./testdata")
	if err != nil {
		t.Fatalf("normalizeRepoURL failed: %v", err)
	}
	if got.Scheme != "file" || got.Path == "" {
		t.Errorf("a local path should be converted into a file address, got %q", got.String())
	}
}

func TestResolveLocation(t *testing.T) {
	base, err := url.Parse("https://mirror.example.com/centos/7/x86_64/")
	if err != nil {
		t.Fatalf("parsing the base address failed: %v", err)
	}
	cases := []struct {
		location Location
		want     string
	}{
		{Location{Href: "Packages/nginx-1.24.0-1.el9.x86_64.rpm"},
			"https://mirror.example.com/centos/7/x86_64/Packages/nginx-1.24.0-1.el9.x86_64.rpm"},
		{Location{Href: "repodata/abc-primary.xml.gz"}, "https://mirror.example.com/centos/7/x86_64/repodata/abc-primary.xml.gz"},
		{Location{Href: "https://cdn.example.com/nginx.rpm"}, "https://cdn.example.com/nginx.rpm"},
		{Location{Href: "Packages/a.rpm", Base: "../other/"}, "https://mirror.example.com/centos/7/other/Packages/a.rpm"},
	}
	for _, tc := range cases {
		got, err := resolveLocation(base, tc.location)
		if err != nil {
			t.Errorf("resolveLocation(%+v) failed: %v", tc.location, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveLocation(%+v) = %q, want %q", tc.location, got, tc.want)
		}
	}
}
