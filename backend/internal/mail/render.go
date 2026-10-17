package mail

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
)

// The emails the site sends are HTML files rather than strings in the code that
// decides to send them, so the wording of each one is a file of its own and all
// of them are set in the same shell. A message template is only what that
// message says; the shell carries the brand and the surface around it.
//
//go:embed templates/*.html
var files embed.FS

// Parsed once, when the process starts. A template that does not parse is a
// mistake in the code rather than anything a request can cause, so it is worth
// stopping over rather than reporting to somebody who cannot fix it.
var templates = template.Must(template.New("mail").Funcs(template.FuncMap{
	"nl2br": nl2br,
}).ParseFS(files, "templates/*.html"))

// Render fills one message template and sets it inside the shell. The name is the
// template's file name without the extension, which is the name it defines.
func Render(name string, data any) (string, error) {
	var body bytes.Buffer
	if err := templates.ExecuteTemplate(&body, name, data); err != nil {
		return "", fmt.Errorf("mail: render %s: %w", name, err)
	}

	var out bytes.Buffer
	shell := struct{ Body template.HTML }{Body: template.HTML(body.String())}
	if err := templates.ExecuteTemplate(&out, "shell", shell); err != nil {
		return "", fmt.Errorf("mail: render the shell: %w", err)
	}
	return out.String(), nil
}

// nl2br keeps the line breaks somebody typed into a form. A template escapes the
// text on its own; this only puts the breaks back as markup, so the escaping
// happens here first and the result is markup on purpose.
func nl2br(text string) template.HTML {
	escaped := template.HTMLEscapeString(text)
	escaped = strings.ReplaceAll(escaped, "\r\n", "\n")
	return template.HTML(strings.ReplaceAll(escaped, "\n", "<br />"))
}
