package cfn

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"text/template"
)

//go:embed template.yaml.tmpl
var templateSource string

// RenderOptions controls which variants the rendered template includes.
// A zero value (or nil Variants) means AllVariants.
type RenderOptions struct {
	Variants []RunnerVariant
}

// templateData is the value passed to the text/template executor.
type templateData struct {
	Variants []RunnerVariant
}

// Render writes the CloudFormation template to w.
func Render(w io.Writer, opts RenderOptions) error {
	variants := opts.Variants
	if len(variants) == 0 {
		variants = AllVariants
	}
	tmpl, err := template.New("cfn").Parse(templateSource)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	if err := tmpl.Execute(w, templateData{Variants: variants}); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}
	return nil
}

// RenderBytes renders the template and returns the output as a byte slice.
func RenderBytes(opts RenderOptions) ([]byte, error) {
	var buf bytes.Buffer
	if err := Render(&buf, opts); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
