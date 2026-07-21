package build

import (
	"bytes"
	"html/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

var (
	markdown  = goldmark.New()
	sanitizer = bluemonday.UGCPolicy()
)

// markdownHTML renders trusted Markdown to sanitized HTML for story text blocks.
func markdownHTML(src string) template.HTML {
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src))
	}
	return template.HTML(sanitizer.SanitizeBytes(buf.Bytes()))
}
