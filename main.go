package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	sprig "github.com/go-task/slim-sprig/v3"
	"github.com/quintans/faults"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

func main() {
	// Command-line flags
	modelFile := flag.String("model", "", "YAML model file")
	templateDir := flag.String("template", "", "Template directory")
	outputDir := flag.String("out", "", "Output directory")
	dryRun := flag.Bool("dry-run", false, "Print actions without writing files")
	flag.Parse()

	// Load model from YAML file
	model, err := LoadModel(*modelFile)
	noError(err, "failed to load model: %+v", err)

	info, err := os.Stat(*templateDir)
	noError(err, "template dir error: %+v", err)
	if !info.IsDir() {
		fatalf("template path must be a directory")
	}

	// Ensure output directory exists (or would exist in dry-run mode)
	if *dryRun {
		fmt.Printf("DRY-RUN: would ensure output dir %s exists\n", *outputDir)
	} else {
		err = os.MkdirAll(*outputDir, 0o755)
		noError(err, "failed to create output dir: %+v", err)
	}

	cc := CopyCat{
		Model:      model,
		DryRun:     *dryRun,
		TemplateFS: afero.NewOsFs(),
		OutputFS:   afero.NewOsFs(),
	}

	err = cc.ProcessDir(*templateDir, *outputDir, model)
	noError(err, "failed to process directory: %+v", err)

	if *dryRun {
		fmt.Println("Dry-run complete. No files written.")
	} else {
		fmt.Println("Template expansion complete.")
	}
}

type CopyCat struct {
	Model       map[string]any
	DryRun      bool
	TemplateFS  afero.Fs
	OutputFS    afero.Fs
	CustomFuncs template.FuncMap
}

// LoadModel reads a YAML file into a map
func LoadModel(filename string) (map[string]any, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, faults.Wrap(err)
	}

	var model map[string]any
	if err := yaml.Unmarshal(data, &model); err != nil {
		return nil, faults.Wrap(err)
	}
	return model, nil
}

// ProcessDir processes a template directory and writes output to outFS
//
// This function is made public to allow creating other projects to call it directly.
func (cc *CopyCat) ProcessDir(currentTemplatePath string, currentOutPath string, ctx any) error {
	entries, err := afero.ReadDir(cc.TemplateFS, currentTemplatePath) // Pre-check to ensure templatePath exists
	if err != nil {
		return faults.Wrap(err)
	}

	for _, entry := range entries {
		expanded, err := expandPath(entry.Name(), ctx)
		if err != nil {
			return faults.Wrap(err)
		}

		for _, item := range expanded {
			outPath := filepath.Join(currentOutPath, item.value)

			if entry.IsDir() {
				if cc.DryRun {
					fmt.Printf("[DIR]  %s\n", outPath)
				} else {
					if err := cc.OutputFS.MkdirAll(outPath, 0755); err != nil {
						return faults.Wrap(err)
					}
				}
				err = cc.ProcessDir(filepath.Join(currentTemplatePath, entry.Name()), outPath, item.ctx)
				if err != nil {
					return faults.Wrap(err)
				}

				// After processing the directory, check if it is empty and remove if so
				// We do this here to avoid removing directories that were not created by copycat
				if !cc.DryRun {
					subEntries, err := afero.ReadDir(cc.OutputFS, outPath)
					if err != nil {
						return faults.Wrap(err)
					}
					if len(subEntries) == 0 {
						if err := cc.OutputFS.Remove(outPath); err != nil {
							return faults.Wrap(err)
						}
					}
				}

				continue
			}

			data, err := afero.ReadFile(cc.TemplateFS, filepath.Join(currentTemplatePath, entry.Name()))
			if err != nil {
				return faults.Wrap(err)
			}

			content, err := cc.renderContent(string(data), item.ctx)
			if err != nil {
				return faults.Wrap(err)
			}

			if content == "" {
				if cc.DryRun {
					fmt.Printf("[SKIP] %s (empty after rendering)\n", outPath)
				}
				// if the file exists from a previous run, remove it
				if !cc.DryRun {
					if exists, err := afero.Exists(cc.OutputFS, outPath); exists {
						if err != nil {
							return faults.Wrap(err)
						}
						// Remove the existing file
						if err = cc.OutputFS.Remove(outPath); err != nil {
							return faults.Wrap(err)
						}
					}
				}
				// Skip creating empty files
				continue
			}

			outPath = strings.TrimSuffix(outPath, ".tmpl")
			if cc.DryRun {
				fmt.Printf("[FILE] %s (%d bytes)\n", outPath, len(content))
				continue
			}
			// Write the rendered content to the output file
			if err := afero.WriteFile(cc.OutputFS, outPath, []byte(content), 0755); err != nil {
				return faults.Wrap(err)
			}
		}
	}
	return nil
}

type expandedPath struct {
	value string
	ctx   any
}

// expandPath expands placeholders and carries context for each expansion
func expandPath(path string, ctx any) ([]expandedPath, error) {
	re := regexp.MustCompile(`\{\{\s*([^}]+)\s*\}\}`)
	matches := re.FindAllStringSubmatch(path, -1)

	if len(matches) == 0 {
		// No placeholders, return as-is
		return []expandedPath{{value: path, ctx: ctx}}, nil
	}

	candidates := []expandedPath{{value: path, ctx: ctx}}

	for _, match := range matches {
		placeholder := match[0]
		keyPath := strings.Split(match[1], ".")
		// trim spaces in keyPath elements
		for i := range keyPath {
			keyPath[i] = strings.TrimSpace(keyPath[i])
		}

		var newCandidates []expandedPath
		for _, cand := range candidates {
			values := resolveKeyPathWithContext(cand.ctx, cand.ctx, keyPath)
			if len(values) == 0 {
				continue
			}

			for _, v := range values {
				if isScalar(v.result) {
					newCandidates = append(newCandidates, expandedPath{
						value: strings.ReplaceAll(cand.value, placeholder, fmt.Sprint(v.result)),
						ctx:   v.ctx,
					})
				} else {
					// if not scalar, context is object/array element
					newCandidates = append(newCandidates, expandedPath{
						value: cand.value,
						ctx:   v.ctx,
					})
				}
			}
		}
		candidates = newCandidates
	}

	return candidates, nil
}

type pathContext struct {
	result any
	ctx    any
}

// resolveKeyPathWithContext walks context and returns scalars or objects for expansion
func resolveKeyPathWithContext(parent, data any, keys []string) []pathContext {
	if len(keys) == 0 {
		return []pathContext{{result: data, ctx: parent}}
	}

	key := keys[0]
	switch v := data.(type) {
	case map[string]any:
		if val, ok := v[key]; ok {
			return resolveKeyPathWithContext(v, val, keys[1:])
		}
	case []any:
		var results []pathContext
		for _, item := range v {
			res := resolveKeyPathWithContext(parent, item, keys)
			results = append(results, res...)
		}
		return results
	}
	return nil
}

func isScalar(v any) bool {
	switch v.(type) {
	case string,
		uint8, uint16, uint32, uint64,
		int, int8, int16, int32, int64,
		float32, float64, bool:
		return true
	default:
		return false
	}
}

// renderContent renders the file content template using Go text/template with sprig.
// Data model: . is the current context; root is the root model;
func (cc *CopyCat) renderContent(content string, ctx any) (string, error) {
	funcs := sprig.TxtFuncMap()
	// helper funcs to access root/current contexts regardless of dot
	funcs["root"] = func() any { return cc.Model }
	// apply custom funcs if any
	for k, v := range cc.CustomFuncs {
		funcs[k] = v
	}
	t, err := template.New("file").Funcs(funcs).Option("missingkey=error").Parse(content)
	if err != nil {
		return "", faults.Wrap(err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", faults.Wrap(err)
	}
	return buf.String(), nil
}

func noError(err error, format string, a ...any) {
	if err == nil {
		return
	}

	fatalf(format, a...)
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}
