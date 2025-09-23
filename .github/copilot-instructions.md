# Copilot Instructions for copycat

## Project Overview

copycat is a Go template engine that expands directory structures and files using YAML models. It processes template directories with Go template syntax and placeholder variables to generate customized project scaffolds.

**Key Architecture:**
- CLI tool in `cmd/copycat/` with core logic in `copycat.go` package
- Uses `CopyCat` struct with `NewCopyCat()` constructor and `Run()` method
- Uses `afero.Fs` abstraction for filesystem operations (enables in-memory testing)
- Template expansion supports both scalar replacements and array iteration with context switching
- Main processing happens in `CopyCat.processDir()` which recursively walks template directories

## Core Template System

**Path Expansion Pattern (`{{ variableName }}`):**
- Directory/file names with `{{ features.name }}` create multiple outputs from arrays
- Each array element becomes a new directory with that element as template context
- Example: `{{ features.name }}/{{ name }}.go.tmpl` → `auth/auth.go`, `payments/payments.go`
- Supports Sprig functions: `{{ projectSlug }}` with `"{{ .projectName | slugify }}"` in model

**Template Context System:**
- `{{ . }}` refers to current context (could be array element or root model)
- `{{ (root) }}` helper function always accesses the full YAML model
- Context switches when iterating through arrays in path names

**File Processing Rules:**
- `.tmpl` suffix is automatically stripped from output filenames
- Empty files after template rendering are skipped (not created)
- Empty directories are automatically removed after processing

## Development Workflows

**Testing with Examples:**
```bash
# Run with the provided example
go run cmd/copycat/main.go -model examples/model.yaml -template examples/template -out ./output -dry-run

# Run actual tests
go test -v
```

**Key Test Patterns:**
- Use `afero.NewMemMapFs()` for in-memory filesystem testing
- Test both dry-run and actual file creation modes
- Verify context switching in array iterations (see `TestExpandPathSegmentArray`)
- Test empty file/directory cleanup behavior

## Project-Specific Conventions

**Error Handling:**
- Use `github.com/quintans/faults` for error wrapping, not standard errors
- Fatal errors use `noError()` helper which calls `fatalf()` and `os.Exit(1)`
- Template parsing errors should include context about which file failed

**Template Function Extensions:**
- Sprig functions are available via `github.com/go-task/slim-sprig/v3`
- Custom `root()` function provides access to full model from any context
- Support for custom functions via `WithCustomFuncs()` option
- Use `missingkey=error` option to fail fast on undefined variables

**Filesystem Abstraction:**
- Always use `afero.Fs` interface, never direct `os` package calls
- `CopyCat.Run()` is public API for integration with other tools
- `CopyCat.processDir()` handles recursive template processing
- Support both real filesystem and in-memory for testing

## Key Files and Patterns

**cmd/copycat/main.go** - CLI entry point:
- Command-line flag parsing
- `LoadModel()` for YAML file loading
- `NewCopyCat()` constructor with filesystem interfaces
- `CopyCat.Run()` execution with dry-run support

**copycat.go** - Core logic package:
- `LoadModel()`: YAML unmarshaling into `map[string]any`
- `CopyCat` struct with template and output filesystems
- `NewCopyCat()`: Constructor with optional custom functions
- `CopyCat.Run()`: Main processing entry point
- `CopyCat.processDir()`: Recursive template processing with context switching
- `expandPath()`: Path placeholder expansion with array iteration
- `renderContent()`: Go template rendering with sprig + custom functions

**copycat_test.go** - Comprehensive test coverage:
- Integration tests using real example data
- Path expansion and context switching verification
- Template rendering with context access
- Dry-run mode validation

**examples/** - Working template system demonstrating all features:
- `model.yaml`: Sample data model with arrays, nested objects, and Sprig functions
- `template/{{ projectSlug }}/`: Shows directory name templating with slugified names
- `{{ features.name }}/{{ name }}.go.tmpl`: Demonstrates array iteration with context switching

## Integration Points

**External Dependencies:**
- `spf13/afero`: Filesystem abstraction layer
- `go-task/slim-sprig/v3`: Template helper functions
- `gopkg.in/yaml.v3`: YAML model parsing
- `quintans/faults`: Enhanced error handling

**CLI Interface:**
- Required: `-model`, `-template`, `-out` flags
- Optional: `-dry-run` for preview mode
- Exit codes: 0 success, 1 any error

**Public API:**
- `LoadModel()` function for YAML file loading
- `NewCopyCat()` constructor accepts filesystem interfaces and options
- `CopyCat.Run()` method can be imported and used by other Go programs
- `WithCustomFuncs()` option for extending template functions
- Accepts `afero.Fs` interfaces for custom filesystem implementations