package service

import (
	"strings"
	"testing"
)

func TestAPublishedFileIsTypedByItsName(t *testing.T) {
	for _, tc := range []struct{ filename, content, want string }{
		// Sniffed, this one reads as HTML; its name says Markdown.
		{"advisory-S1.md", "<!-- private -->\n# Advisory\n", "text/markdown"},
		{"NOTES.MARKDOWN", "# Notes\n", "text/markdown"},
		{"report.html", "<h1>Report</h1>", "text/html"},
		{"unnamed", "<html><body>x</body></html>", "text/html"},
		{"unnamed", "plain words", "text/plain"},
	} {
		if got := publishedType(tc.filename, []byte(tc.content)); !strings.HasPrefix(got, tc.want) {
			t.Errorf("publishedType(%q) = %q, want %s", tc.filename, got, tc.want)
		}
	}
}
