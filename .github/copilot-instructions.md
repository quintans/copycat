# Copycat Project - AI Assistant Instructions

## Project Overview

`copycat` is a Go template expansion utility that copies directories while dynamically expanding folder/file names and content based on YAML models. The core concept is **context-aware template expansion** where array fields in the model create multiple instances with each array element becoming the rendering context.

## Architecture & Key Components

### Core Flow
1. **Model Loading**: YAML model loaded into `map[string]any` with type normalization
2. **Template Walking**: Recursive directory traversal processes each path segment
3. **Path Expansion**: `{{ ... }}` expressions in paths create multiple outputs when resolving to arrays
4. **Context Propagation**: Array elements become the template context (`.`) for nested paths and file content
5. **Content Rendering**: Files are rendered using Go `text/template` with sprig functions
6. **Empty Content Cleanup**: Empty files are skipped and empty directories are removed automatically

### Key Functions
- `expandPathSegment()`: Core logic for path expansion and context switching
- `evaluatePathNodes()`: Broadcasts field access across arrays while preserving element context
- `renderContent()`: Renders file templates with current context as `.` and root model via `(root)`
- `removeEmptyDirs()`: Post-processing cleanup that removes directories left empty after file skipping

## Template System Conventions

### Path Expressions
- Use `{{ features.name }}` to expand one directory/file per feature
- Use `{{ projectName }}` for scalar values that don't expand
- Expressions resolve relative to current context first, then root model
- Arrays in path segments create multiplicative expansion (one path per element)
- **Empty arrays result in no expansion** - no files or directories are created for that branch
- **Empty files are automatically skipped** - content that renders to only whitespace is not written

### Template Functions in Content
- `(root)` or `root`: Access root YAML model from any context
- `.`: Access current context
- Full sprig function library available
- Example: `{{ (root).projectName }}` or `{{ root.projectName }}` gets project name from any nested context

### File Naming
- Template files should end in `.tmpl` (automatically stripped in output)
- Non-template files are copied as-is but names can still contain expressions

## Development Workflows

### Testing Template Expansion
```bash
# Always use dry-run first to verify expansion logic
./copycat --model examples/model.yaml --template examples/template --out ./test-output --dry-run

# Then run actual generation
./copycat --model examples/model.yaml --template examples/template --out ./test-output
```

### Building and Running
```bash
go build -o copycat .
./copycat --model <model.yaml> --template <template_dir> --out <output_dir>
```

## Project-Specific Patterns

### Model Structure Expectations
- Root-level scalar fields (e.g., `projectName`, `owner.name`) for project-wide values
- Array fields (e.g., `features[]`) for generating multiple similar structures
- Each array element should have consistent field structure for template rendering

### Template Organization
- Templates follow the pattern: `{{ projectName }}/{{ features.name }}/{{ name }}.go.tmpl`
- This creates: one project dir → multiple feature dirs → multiple files per feature
- Context flows: root → feature element → feature element (for inner expressions)

### Empty Content Handling
- Files that render to empty content (after `strings.TrimSpace()`) are automatically skipped
- Directories that become empty after file skipping are removed via `removeEmptyDirs()`
- Empty arrays in path expressions result in no expansion (no files/directories created)
- Dry-run mode shows `SKIP filename (empty after rendering)` for skipped files

### Error Handling Strategy
- Uses `github.com/quintans/faults` for error wrapping with stack traces
- Fail fast on invalid models, missing templates, or filesystem errors
- Template rendering errors include file context for debugging

## Dependencies & External Integration

### Key Dependencies
- `gopkg.in/yaml.v3`: YAML model parsing with flexible type handling
- `github.com/Masterminds/sprig/v3`: Template function library
- `github.com/quintans/faults`: Enhanced error handling with stack traces

### Integration Points
- **Input**: YAML model files define expansion data
- **Input**: Template directories with `{{ ... }}` expressions in paths/content
- **Output**: Generated directory structures with expanded content
- **CLI**: Standard flag-based interface for model, template, output paths

## Critical Implementation Details

### Type Normalization
The `normalize()` function converts YAML's `map[any]any` to `map[string]any` recursively - essential for consistent template evaluation.

### Context Switching
When `{{ features.name }}` expands in a path, each resulting path uses that feature element as the template context (`.`), not the root model. This enables `{{ .table }}` to access the current feature's table field.

**Critical Fix**: Single-node path expressions now properly propagate context changes by treating them as single-element lists rather than scalars.

### Expression Evaluation Order
1. Try structured path traversal with context mapping (`evaluatePathNodes`)
2. Fall back to full template rendering with sprig functions
3. Root model always accessible via helper functions regardless of current context

### Empty Content Processing
1. All files are rendered through `renderContent()` regardless of template status
2. Files with empty content (after whitespace trimming) are skipped
3. Empty directories are removed in post-processing using bottom-up traversal

## When Contributing

- Follow the Go instruction file at `.github/instructions/go.instructions.md`
- Test expansions with both scalar and array model fields
- Verify context propagation through nested template expressions
- Use dry-run mode extensively during development
- **Test empty content scenarios**: Verify empty files are skipped and empty directories are removed
- **Validate edge cases**: empty arrays, missing fields, deeply nested structures, mixed empty/non-empty content
- Use testify `require` for critical assertions and `assert` for validation checks