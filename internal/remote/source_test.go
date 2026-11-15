package remote

import (
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

func TestParseSource(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		url     string
		ref     string
		wantErr string
	}{
		{
			name: "github defaults to HEAD and enver.yaml",
			raw:  "github:u/r",
			url:  "https://raw.githubusercontent.com/u/r/HEAD/enver.yaml",
			ref:  "HEAD",
		},
		{
			name: "github defaults to HEAD with a path",
			raw:  "github:u/r/models.yaml",
			url:  "https://raw.githubusercontent.com/u/r/HEAD/models.yaml",
			ref:  "HEAD",
		},
		{
			name: "github defaults to enver.yaml with a ref",
			raw:  "github:u/r@v2",
			url:  "https://raw.githubusercontent.com/u/r/v2/enver.yaml",
			ref:  "v2",
		},
		{
			name: "github path and ref",
			raw:  "github:u/r/models.yaml@v2",
			url:  "https://raw.githubusercontent.com/u/r/v2/models.yaml",
			ref:  "v2",
		},
		{
			name: "github nested path and commit ref",
			raw:  "github:u/r/profiles/models.yaml@0123abc",
			url:  "https://raw.githubusercontent.com/u/r/0123abc/profiles/models.yaml",
			ref:  "0123abc",
		},
		{
			name: "https passes through",
			raw:  "https://example.com/c.yaml",
			url:  "https://example.com/c.yaml",
		},
		{
			name: "https splits a trailing ref",
			raw:  "https://example.com/c.yaml@v2",
			url:  "https://example.com/c.yaml",
			ref:  "v2",
		},
		{
			name: "https keeps userinfo out of the ref split",
			raw:  "https://user@example.com/c.yaml",
			url:  "https://user@example.com/c.yaml",
		},
		{
			name: "https userinfo without a path is not a ref",
			raw:  "https://user@example.com",
			url:  "https://user@example.com",
		},
		{
			name: "https splits a ref off a path that follows userinfo",
			raw:  "https://user@example.com/c.yaml@v2",
			url:  "https://user@example.com/c.yaml",
			ref:  "v2",
		},
		{
			name: "https keeps an @ in the query",
			raw:  "https://example.com/c.yaml?sig=abc@123",
			url:  "https://example.com/c.yaml?sig=abc@123",
		},
		{
			name: "https keeps an @ in the fragment",
			raw:  "https://example.com/c.yaml#a@b",
			url:  "https://example.com/c.yaml#a@b",
		},
		{
			name: "https splits a ref off before the query",
			raw:  "https://example.com/c.yaml@v2?x=1",
			url:  "https://example.com/c.yaml?x=1",
			ref:  "v2",
		},
		{
			name: "https keeps an @ that opens a directory",
			raw:  "https://example.com/@types/env.yaml",
			url:  "https://example.com/@types/env.yaml",
		},
		{
			name: "scheme case does not matter",
			raw:  "GitHub:u/r@v2",
			url:  "https://raw.githubusercontent.com/u/r/v2/enver.yaml",
			ref:  "v2",
		},
		{
			name:    "empty source",
			raw:     "",
			wantErr: "no scheme",
		},
		{
			name:    "http is not fetched",
			raw:     "http://example.com/c.yaml",
			wantErr: "http",
		},
		{
			name:    "http is rejected whatever its case",
			raw:     "HTTP://example.com/c.yaml",
			wantErr: "http",
		},
		{
			name:    "unknown scheme",
			raw:     "gitlab:user/repo",
			wantErr: `"gitlab"`,
		},
		{
			name:    "schemeless source",
			raw:     "example.com/c.yaml",
			wantErr: "no scheme",
		},
		{
			name:    "github without a repo",
			raw:     "github:u",
			wantErr: "owner/repo",
		},
		{
			name:    "github with an empty ref",
			raw:     "github:u/r@",
			wantErr: "empty ref",
		},
		{
			name:    "github with an empty path segment",
			raw:     "github:u/r/",
			wantErr: `segment ""`,
		},
		{
			name:    "github ref with a slash",
			raw:     "github:u/r/models.yaml@release/v1",
			wantErr: `pins ref "release/v1"`,
		},
		{
			name:    "https ref with a slash",
			raw:     "https://example.com/c.yaml@release/v1",
			wantErr: `pins ref "release/v1"`,
		},
		{
			name:    "github owner with a space",
			raw:     "github:u r/models.yaml",
			wantErr: "segment",
		},
		{
			name:    "github path may not step up",
			raw:     "github:u/r/../x.yaml",
			wantErr: `".."`,
		},
		{
			name:    "github path may not step down",
			raw:     "github:u/r/./x.yaml",
			wantErr: `"."`,
		},
		{
			name:    "github path segment may not hold an @",
			raw:     "github:u/r/@types/env.yaml",
			wantErr: `"@types"`,
		},
		{
			name:    "https refuses an @ inside a path it cannot read",
			raw:     "https://example.com/dir@x/file.yaml",
			wantErr: `pins ref "x/file.yaml"`,
		},
		{
			name:    "https without a host",
			raw:     "https://",
			wantErr: "host",
		},
		{
			name:    "https with an empty ref",
			raw:     "https://example.com/c.yaml@",
			wantErr: "empty ref",
		},
		{
			name:    "https with an unescaped segment",
			raw:     "https://example.com/%zz",
			wantErr: "not a URL",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, ref, err := ParseSource(tc.raw)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseSource(%q) = %q, %q, want error", tc.raw, url, ref)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseSource(%q) err = %v, want it to mention %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSource(%q) err = %v", tc.raw, err)
			}
			if url != tc.url || ref != tc.ref {
				t.Fatalf("ParseSource(%q) = (%q, %q), want (%q, %q)", tc.raw, url, ref, tc.url, tc.ref)
			}
		})
	}
}

func TestResolveRecord(t *testing.T) {
	tests := []struct {
		name    string
		rec     config.RemoteRecord
		url     string
		ref     string
		wantErr string
	}{
		{
			name: "record ref wins over an inline one",
			rec:  config.RemoteRecord{URL: "https://example.com/c.yaml@v2", Ref: "v3"},
			url:  "https://example.com/c.yaml",
			ref:  "v3",
		},
		{
			name: "record ref rewrites a github raw path",
			rec:  config.RemoteRecord{URL: "github:u/r/models.yaml@v2", Ref: "v3"},
			url:  "https://raw.githubusercontent.com/u/r/v3/models.yaml",
			ref:  "v3",
		},
		{
			name: "record ref on a plain source is a label, not a rewrite",
			rec:  config.RemoteRecord{URL: "https://example.com/c.yaml", Ref: "v3"},
			url:  "https://example.com/c.yaml",
			ref:  "v3",
		},
		{
			name: "record ref does not disturb an @ in the query",
			rec:  config.RemoteRecord{URL: "https://example.com/c.yaml?sig=a@1", Ref: "v3"},
			url:  "https://example.com/c.yaml?sig=a@1",
			ref:  "v3",
		},
		{
			name: "inline ref stands in when the record has none",
			rec:  config.RemoteRecord{URL: "github:u/r@v2"},
			url:  "https://raw.githubusercontent.com/u/r/v2/enver.yaml",
			ref:  "v2",
		},
		{
			name: "github record with no ref at all tracks HEAD",
			rec:  config.RemoteRecord{URL: "github:u/r"},
			url:  "https://raw.githubusercontent.com/u/r/HEAD/enver.yaml",
			ref:  "HEAD",
		},
		{
			name:    "record ref with a slash cannot be carried",
			rec:     config.RemoteRecord{URL: "github:u/r", Ref: "release/v1"},
			wantErr: `pins ref "release/v1"`,
		},
		{
			name:    "record URL is still parsed",
			rec:     config.RemoteRecord{URL: "gitlab:u/r"},
			wantErr: `"gitlab"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, ref, err := ResolveRecord(tc.rec)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolveRecord(%+v) = %q, %q, want error", tc.rec, url, ref)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ResolveRecord(%+v) err = %v, want it to mention %q", tc.rec, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRecord(%+v) err = %v", tc.rec, err)
			}
			if url != tc.url || ref != tc.ref {
				t.Fatalf("ResolveRecord(%+v) = (%q, %q), want (%q, %q)", tc.rec, url, ref, tc.url, tc.ref)
			}
		})
	}
}

func TestUnparsedSourceDoesNotResolve(t *testing.T) {
	var zero source
	url, ref, err := zero.resolve("")
	if err == nil {
		t.Fatalf("zero source resolved to (%q, %q), want an error", url, ref)
	}
}
