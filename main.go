package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/quintans/faults"
	"gopkg.in/yaml.v3"
)

// Contract
// Inputs: YAML model path, template root dir, output dir, dry-run flag
// Behavior: Copy template directory to output while expanding file/folder names that contain {{ ... }} expressions.
// - If an expression inside a path segment resolves to an array/slice, expand that segment into multiple files/folders (one per element),
//   and set that element as the "current context" for rendering deeper path segments and contents.
// - Expressions can refer to any field in the model, e.g. {{ projectName }} or {{ features.name }}. If the expression resolves to a scalar, render it directly.
// - File contents are rendered using Go text/template with sprig functions, using the current context merged with root model under
//   . (current) and 'root' (root model).
// - If --dry-run is set, only print the operations that would occur without writing files.

var (
	modelPath    = flag.String("model", "", "Path to YAML model file")
	templateRoot = flag.String("template", "", "Path to template directory")
	outRoot      = flag.String("out", "", "Output directory")
	dryRun       = flag.Bool("dry-run", false, "If set, only print planned operations")
)

func main() {
	flag.Parse()
	if *modelPath == "" || *templateRoot == "" || *outRoot == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s --model model.yaml --template template_dir --out out_dir [--dry-run]\n", filepath.Base(os.Args[0]))
		os.Exit(2)
	}

	model, err := loadYAMLModel(*modelPath)
	if err != nil {
		fatalf("failed to load model: %+v", err)
	}

	info, err := os.Stat(*templateRoot)
	if err != nil {
		fatalf("template dir error: %+v", err)
	}
	if !info.IsDir() {
		fatalf("template path must be a directory")
	}

	// Walk the template directory and process entries depth-first.
	createdDirs := make(map[string]struct{})
	err = processDir(*templateRoot, "", *outRoot, "", model, model, *dryRun, createdDirs)
	if err != nil {
		fatalf("processing failed: %+v", err)
	}

	// Remove empty directories after processing (only in non-dry-run mode)
	if !*dryRun {
		err = removeEmptyCreatedDirs(createdDirs)
		if err != nil {
			fatalf("failed to remove empty directories: %+v", err)
		}
	}
}

// removeEmptyCreatedDirs removes only directories that were created during this copycat run
// and have become empty after file processing.
func removeEmptyCreatedDirs(createdDirs map[string]struct{}) error {
	// Convert map to slice and sort by depth (deepest first)
	var dirs []string
	for dir := range createdDirs {
		dirs = append(dirs, dir)
	}

	// Sort directories by depth (deepest first) to process bottom-up
	// Longer paths are deeper in the directory tree
	for i := 0; i < len(dirs); i++ {
		for j := i + 1; j < len(dirs); j++ {
			if len(dirs[i]) < len(dirs[j]) {
				dirs[i], dirs[j] = dirs[j], dirs[i]
			}
		}
	}

	// Remove empty directories bottom-up
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// If we can't read the directory, skip it
			continue
		}

		if len(entries) == 0 {
			if err := os.Remove(dir); err != nil {
				// If we can't remove it, continue with other directories
				continue
			}
		}
	}

	return nil
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

// loadYAMLModel loads a YAML file into a generic map[string]any with normalized types.
func loadYAMLModel(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, faults.Wrap(err)
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return nil, faults.Wrap(err)
	}
	return normalize(v).(map[string]any), nil
}

// normalize converts YAML-generic structures into Go-native map[string]any recursively.
func normalize(v any) any {
	switch t := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, vv := range t {
			m[fmt.Sprintf("%v", k)] = normalize(vv)
		}
		return m
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, vv := range t {
			m[k] = normalize(vv)
		}
		return m
	case []any:
		arr := make([]any, len(t))
		for i, vv := range t {
			arr[i] = normalize(vv)
		}
		return arr
	default:
		return v
	}
}

// processDir processes the template directory at rel path, creating outputs under outRoot.
func processDir(templateRoot string, srcRel string, outRoot string, outRel string, rootModel map[string]any, ctx any, dry bool, createdDirs map[string]struct{}) error {
	currentTemplatePath := filepath.Join(templateRoot, srcRel)

	entries, err := os.ReadDir(currentTemplatePath)
	if err != nil {
		return faults.Wrap(err)
	}

	// For deterministic behavior, we can rely on ReadDir being sorted lexicographically.
	for _, entry := range entries {
		name := entry.Name()
		// Expand the name (which may contain template expressions) into zero or more names and contexts.
		expanded, err := expandPathSegment(name, rootModel, ctx)
		if err != nil {
			return faults.Errorf("expand segment %q: %w", name, err)
		}
		for _, seg := range expanded {
			srcPath := filepath.Join(templateRoot, srcRel, name)

			if entry.IsDir() {
				newOutRel := filepath.Join(outRel, seg.Rendered)
				targetOutPath := filepath.Join(outRoot, newOutRel)
				if dry {
					fmt.Printf("MKDIR %s\n", targetOutPath)
				} else {
					if err := os.MkdirAll(targetOutPath, 0o755); err != nil {
						return faults.Wrap(err)
					}
					// Track all directories in the path as created by copycat
					// MkdirAll creates intermediate directories, so we need to track them all
					currentPath := outRoot
					pathParts := strings.Split(strings.Trim(newOutRel, string(filepath.Separator)), string(filepath.Separator))
					for _, part := range pathParts {
						if part != "" {
							currentPath = filepath.Join(currentPath, part)
							createdDirs[currentPath] = struct{}{}
						}
					}
				}
				// Recurse into directory with updated context
				if err := processDir(templateRoot, filepath.Join(srcRel, name), outRoot, newOutRel, rootModel, seg.Ctx, dry, createdDirs); err != nil {
					return faults.Wrap(err)
				}
			} else {
				// File: render content with seg.Ctx
				data, err := os.ReadFile(srcPath)
				if err != nil {
					return faults.Wrap(err)
				}
				rendered, err := renderContent(string(data), rootModel, seg.Ctx)
				if err != nil {
					return faults.Errorf("render file %s: %w", srcPath, err)
				}

				outName := seg.Rendered
				if strings.HasSuffix(name, ".tmpl") {
					outName = strings.TrimSuffix(outName, ".tmpl")
				}
				newOutRel := filepath.Join(outRel, outName)
				targetOutPath := filepath.Join(outRoot, newOutRel)

				// Skip empty files
				if strings.TrimSpace(rendered) == "" {
					if dry {
						fmt.Printf("SKIP %s (empty after rendering)\n", targetOutPath)
					}
					continue
				}

				if dry {
					fmt.Printf("WRITE %s (%d bytes)\n", targetOutPath, len(rendered))
				} else {
					parentDir := filepath.Dir(targetOutPath)
					if err := os.MkdirAll(parentDir, 0o755); err != nil {
						return faults.Wrap(err)
					}
					// Track all parent directories as created by copycat
					currentPath := outRoot
					relParentPath, err := filepath.Rel(outRoot, parentDir)
					if err == nil && relParentPath != "." {
						pathParts := strings.Split(strings.Trim(relParentPath, string(filepath.Separator)), string(filepath.Separator))
						for _, part := range pathParts {
							if part != "" {
								currentPath = filepath.Join(currentPath, part)
								createdDirs[currentPath] = struct{}{}
							}
						}
					}
					if err := os.WriteFile(targetOutPath, []byte(rendered), 0o644); err != nil {
						return faults.Wrap(err)
					}
				}
			}
		}
	}
	return nil
}

// segmentExpansion represents one resulting name plus its context after expanding a path segment.
type segmentExpansion struct {
	Rendered string
	Ctx      any
}

var templateExprRe = regexp.MustCompile(`{{[^}]+}}`)

// expandPathSegment evaluates template expressions within a single path segment.
// If any expression resolves to a slice/array, we expand into multiple segments, one per element.
// Nested expansions are handled by iterating over combinations in a left-to-right manner.
func expandPathSegment(segment string, rootModel map[string]any, ctx any) ([]segmentExpansion, error) {
	// Fast path: no template markers
	if !strings.Contains(segment, "{{") {
		return []segmentExpansion{{Rendered: segment, Ctx: ctx}}, nil
	}

	// Extract expressions and evaluate them
	parts := templateExprRe.FindAllStringIndex(segment, -1)
	if len(parts) == 0 {
		// malformed? treat as literal
		return []segmentExpansion{{Rendered: segment, Ctx: ctx}}, nil
	}

	// Build a list of evaluators for each expression
	type exprVal struct {
		raw    string
		isList bool
		list   []any
		scalar string
		ctxs   []any // parallels list when isList
	}

	exprs := make([]exprVal, 0, len(parts))
	last := 0
	literals := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		literals = append(literals, segment[last:p[0]])
		raw := segment[p[0]:p[1]]
		expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "{{"), "}}"))
		// Evaluate as path with context mapping first (relative to current context only)
		if nodes, ok := evaluatePathNodes(ctx, expr); ok {
			if len(nodes) > 1 {
				list := make([]any, 0, len(nodes))
				ctxs := make([]any, 0, len(nodes))
				for _, n := range nodes {
					list = append(list, n.Value)
					if n.Ctx != nil {
						ctxs = append(ctxs, n.Ctx)
					} else {
						ctxs = append(ctxs, n.Value)
					}
				}
				exprs = append(exprs, exprVal{raw: raw, isList: true, list: list, ctxs: ctxs})
			} else if len(nodes) == 1 {
				// Even for single nodes, we need to preserve context changes
				exprs = append(exprs, exprVal{raw: raw, isList: true, list: []any{nodes[0].Value}, ctxs: []any{nodes[0].Ctx}})
			} else {
				// Empty nodes - treat as empty list
				exprs = append(exprs, exprVal{raw: raw, isList: true, list: []any{}, ctxs: []any{}})
			}
		} else {
			// Fall back to template evaluation as scalar
			val, err := evalExpression(expr, rootModel, ctx)
			if err != nil {
				return nil, faults.Wrap(err)
			}
			s, err := toString(val)
			if err != nil {
				return nil, faults.Wrap(err)
			}
			exprs = append(exprs, exprVal{raw: raw, isList: false, scalar: s})
		}
		last = p[1]
	}
	literals = append(literals, segment[last:])

	// Now expand combinations. Start with one seed.
	acc := []segmentExpansion{{Rendered: literals[0], Ctx: ctx}}
	for i, e := range exprs {
		next := make([]segmentExpansion, 0)
		for _, a := range acc {
			if e.isList {
				if len(e.list) == 0 {
					// Empty list: produce no outputs for this branch.
					continue
				}
				for i2, el := range e.list {
					s, err := toString(el)
					if err != nil {
						return nil, faults.Wrap(err)
					}
					rendered := a.Rendered + s + literals[i+1]
					newCtx := a.Ctx
					if len(e.ctxs) > i2 {
						newCtx = e.ctxs[i2]
					}
					next = append(next, segmentExpansion{Rendered: rendered, Ctx: newCtx})
					// Note: context becomes the element for subsequent expressions
				}
			} else {
				rendered := a.Rendered + e.scalar + literals[i+1]
				next = append(next, segmentExpansion{Rendered: rendered, Ctx: a.Ctx})
			}
		}
		acc = next
	}
	// If acc is empty due to empty-list expansions, return empty
	return acc, nil
}

// resolvedNode keeps value and originating context (e.g., array element) for broadcasting paths.
type resolvedNode struct {
	Value any
	Ctx   any
}

// evaluatePathNodes traverses expr as a path from v, broadcasting across arrays and capturing the element as context.
func evaluatePathNodes(v any, expr string) ([]resolvedNode, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" || expr == "." {
		return []resolvedNode{{Value: v, Ctx: v}}, true
	}
	parts := strings.Split(expr, ".")
	nodes := []resolvedNode{{Value: v, Ctx: v}}
	for _, part := range parts {
		var next []resolvedNode
		for _, n := range nodes {
			switch cur := n.Value.(type) {
			case map[string]any:
				if val, ok := cur[part]; ok {
					// context remains the same unless val is selected from an array below
					next = append(next, resolvedNode{Value: val, Ctx: n.Ctx})
				}
			case []any:
				if idx, err := strconv.Atoi(part); err == nil {
					if idx >= 0 && idx < len(cur) {
						el := cur[idx]
						next = append(next, resolvedNode{Value: el, Ctx: el})
					}
				} else {
					// broadcast field access over array elements
					for _, el := range cur {
						if m, ok := el.(map[string]any); ok {
							if val, ok := m[part]; ok {
								next = append(next, resolvedNode{Value: val, Ctx: el})
							}
						}
					}
				}
			}
		}
		if len(next) == 0 {
			// Empty result set - this can happen with empty arrays or missing fields
			// For empty arrays, we want to return empty nodes (not failure)
			// For missing fields, we want to return failure
			// We can distinguish by checking if we successfully found the field but it was empty
			if len(nodes) > 0 {
				// We had valid nodes but got empty results - likely empty array
				return []resolvedNode{}, true
			}
			return nil, false
		}
		nodes = next
	}
	return nodes, true
}

// evalExpression renders a small template expression against the given contexts.
// We support both root model (root) and current context (.)
func evalExpression(expr string, rootModel map[string]any, ctx any) (any, error) {
	// First, try structured traversal that supports arrays (e.g., features.name)
	if v, ok := evaluateModelPath(ctx, expr); ok {
		return v, nil
	}
	if v, ok := evaluateModelPath(rootModel, expr); ok {
		return v, nil
	}
	// Fall back to rendering via text/template which may apply functions, etc., with ctx as dot and root exposed via funcs
	funcs := sprig.TxtFuncMap()
	funcs["root"] = func() any { return rootModel }
	t, err := template.New("expr").Funcs(funcs).Parse("{{ " + expr + " }}")
	if err != nil {
		return nil, faults.Wrap(err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return nil, faults.Wrap(err)
	}
	return buf.String(), nil
}

// evaluateModelPath traverses a dot-separated path through maps and arrays, broadcasting over arrays.
// Returns a scalar value or []any if the path fans out to multiple values. If not resolvable, returns (nil,false).
func evaluateModelPath(v any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return v, true
	}
	parts := strings.Split(path, ".")
	nodes := []any{v}
	for _, part := range parts {
		var next []any
		for _, n := range nodes {
			switch cur := n.(type) {
			case map[string]any:
				if val, ok := cur[part]; ok {
					next = append(next, val)
				}
			case []any:
				if idx, err := strconv.Atoi(part); err == nil {
					if idx >= 0 && idx < len(cur) {
						next = append(next, cur[idx])
					}
				} else {
					for _, el := range cur {
						if m, ok := el.(map[string]any); ok {
							if val, ok := m[part]; ok {
								next = append(next, val)
							}
						}
					}
				}
			}
		}
		if len(next) == 0 {
			return nil, false
		}
		nodes = next
	}
	if len(nodes) == 1 {
		return nodes[0], true
	}
	return nodes, true
}

func toString(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case fmt.Stringer:
		return t.String(), nil
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, bool:
		return fmt.Sprintf("%v", t), nil
	case map[string]any:
		// Try common field "name" for default naming if present
		if name, ok := t["name"]; ok {
			return toString(name)
		}
		return "", faults.New("cannot stringify object without 'name'")
	default:
		return fmt.Sprintf("%v", t), nil
	}
}

// renderContent renders the file content template using Go text/template with sprig.
// Data model: . is the current context; root is the root model;
func renderContent(content string, rootModel map[string]any, ctx any) (string, error) {
	funcs := sprig.TxtFuncMap()
	// helper funcs to access root/current contexts regardless of dot
	funcs["root"] = func() any { return rootModel }
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
