package web

import (
	"io/fs"
	"regexp"
	"testing"
)

// The binary must serve the whole UI itself: in-cluster there may be no
// internet, and the CSP only allows scripts from 'self'.
func TestNoExternalScripts(t *testing.T) {
	index, err := fs.ReadFile(Static, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`<script[^>]+src="(https?:)?//`).Find(index); m != nil {
		t.Fatalf("external script in index.html: %s", m)
	}
	if regexp.MustCompile(`text/babel`).Match(index) {
		t.Fatal("index.html still relies on in-browser Babel")
	}
	for _, f := range []string{"app.js", "three.js", "vendor/react.production.min.js", "vendor/react-dom.production.min.js"} {
		if b, err := fs.ReadFile(Static, f); err != nil || len(b) == 0 {
			t.Fatalf("%s missing from the embed (run `make web`): %v", f, err)
		}
	}
}
