package copycat

import (
	"bytes"
	"fmt"
	"maps"
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

type CopyCat struct {
	templateFS  afero.Fs
	outputFS    afero.Fs
	model       map[string]any
	customFuncs template.FuncMap
}

type Option func(*CopyCat)

func WithCustomFuncs(funcs template.FuncMap) Option {
	return func(cc *CopyCat) {
		cc.customFuncs = funcs
	}
}

func NewCopyCat(templateFS, outputFS afero.Fs, model map[string]any, options ...Option) (*CopyCat, error) {
	cc := &CopyCat{
		model:      model,
		templateFS: templateFS,
		outputFS:   outputFS,
	}
	for _, opt := range options {
		opt(cc)
	}

	m, err := cc.renderModelValue(model, model)
	if err != nil {
		return nil, faults.Wrap(err)
	}
	// if it fails casting, something is very wrong
	cc.model = m.(map[string]any)

	return cc, nil
}

func (cc *CopyCat) renderModelValue(parent, value any) (any, error) {
	switch v := value.(type) {
	case string:
		return cc.renderContent(v, parent)
	case map[string]any:
		newMap := make(map[string]any, len(v))
		for mk, mv := range v {
			renderedVal, err := cc.renderModelValue(v, mv)
			if err != nil {
				return nil, faults.Wrap(err)
			}
			newMap[mk] = renderedVal
		}
		return newMap, nil
	case []any:
		newArr := make([]any, len(v))
		for k, item := range v {
			renderedItem, err := cc.renderModelValue(v, item)
			if err != nil {
				return nil, faults.Wrap(err)
			}
			newArr[k] = renderedItem
		}
		return newArr, nil
	default:
		return v, nil
	}
}

func (cc *CopyCat) Run(templatePath string, outPath string, dryRun bool) error {
	return cc.processDir(templatePath, outPath, cc.model, dryRun)
}

// ProcessDir processes a template directory and writes output to outFS
//
// This function is made public to allow creating other projects to call it directly.
func (cc *CopyCat) processDir(currentTemplatePath string, currentOutPath string, ctx any, dryRun bool) error {
	entries, err := afero.ReadDir(cc.templateFS, currentTemplatePath) // Pre-check to ensure templatePath exists
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
				if dryRun {
					fmt.Printf("[DIR]  %s\n", outPath)
				} else {
					if err := cc.outputFS.MkdirAll(outPath, 0755); err != nil {
						return faults.Wrap(err)
					}
				}
				err = cc.processDir(filepath.Join(currentTemplatePath, entry.Name()), outPath, item.ctx, dryRun)
				if err != nil {
					return faults.Wrap(err)
				}

				// After processing the directory, check if it is empty and remove if so
				// We do this here to avoid removing directories that were not created by copycat
				if !dryRun {
					subEntries, err := afero.ReadDir(cc.outputFS, outPath)
					if err != nil {
						return faults.Wrap(err)
					}
					if len(subEntries) == 0 {
						if err := cc.outputFS.Remove(outPath); err != nil {
							return faults.Wrap(err)
						}
					}
				}

				continue
			}

			data, err := afero.ReadFile(cc.templateFS, filepath.Join(currentTemplatePath, entry.Name()))
			if err != nil {
				return faults.Wrap(err)
			}

			content, err := cc.renderContent(string(data), item.ctx)
			if err != nil {
				return faults.Wrap(err)
			}

			if content == "" {
				if dryRun {
					fmt.Printf("[SKIP] %s (empty after rendering)\n", outPath)
				}
				// if the file exists from a previous run, remove it
				if !dryRun {
					if exists, err := afero.Exists(cc.outputFS, outPath); exists {
						if err != nil {
							return faults.Wrap(err)
						}
						// Remove the existing file
						if err = cc.outputFS.Remove(outPath); err != nil {
							return faults.Wrap(err)
						}
					}
				}
				// Skip creating empty files
				continue
			}

			outPath = strings.TrimSuffix(outPath, ".tmpl")
			if dryRun {
				fmt.Printf("[FILE] %s (%d bytes)\n", outPath, len(content))
				continue
			}
			// Write the rendered content to the output file
			if err := afero.WriteFile(cc.outputFS, outPath, []byte(content), 0755); err != nil {
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
	funcs["root"] = func() any { return cc.model }
	// apply custom funcs if any
	maps.Copy(funcs, cc.customFuncs)
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
