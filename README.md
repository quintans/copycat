# copycat

A Go template engine that expands directory structures and files using YAML models. Generate customized project scaffolds by processing template directories with Go template syntax and placeholder variables.

## Features

- **Path Expansion**: Directory and file names with `{{ variableName }}` placeholders
- **Array Iteration**: Create multiple outputs from single templates using array data
- **Context Switching**: Access both current array element and root model data
- **Smart Cleanup**: Automatically removes empty files and directories
- **Dry Run Mode**: Preview changes without writing files
- **Filesystem Abstraction**: Works with real filesystems or in-memory for testing

## Installation

```bash
go install github.com/quintans/copycat/cmd/copycat@latest
```

Or clone and build:

```bash
git clone https://github.com/quintans/copycat.git
cd copycat
go build -o copycat cmd/copycat/main.go
```

## Quick Start

1. Create a YAML model file:

```yaml
# model.yaml
projectName: My App
projectSlug: "{{ .projectName | slugify }}"
features:
  - name: auth
    table: users
  - name: payments
    table: invoices
owner:
  name: Alice
```

2. Create a template directory structure:

```
template/
└── {{ projectSlug }}/
    ├── README.md
    └── {{ features.name }}/
        ├── {{ name }}.go.tmpl
        └── config.txt
```

3. Run copycat:

```bash
copycat -model model.yaml -template template -out output
```

4. Generated output:

```
output/
└── my-app/
    ├── README.md
    ├── auth/
    │   ├── auth.go
    │   └── config.txt
    └── payments/
        ├── payments.go
        └── config.txt
```

## Template Syntax

### Path Placeholders

Use `{{ variableName }}` in directory and file names:

- `{{ projectSlug }}` → expands to scalar value (e.g., "my-app")
- `{{ features.name }}` → creates multiple directories from array
> NB: `features` is an array that we defined above in the model

### Template Content

Inside template files, use Go template syntax:

```go
package {{ .name }}

// Project: {{ (root).projectName }}
// Feature: {{ .name }}

func TableName() string {
    return "{{ .table }}"
}
```

> Template files can have the extension `.tmpl`, which will be removed on generation

### Context Access

- `{{ . }}` - Current context (array element or root model)
- `{{ (root) }}` - Always accesses the full model
- All [Sprig template functions](https://masterminds.github.io/sprig/) available

## CLI Options

```bash
copycat [options]

Required:
  -model string     Path to YAML model file
  -template string  Path to template directory
  -out string       Output directory path

Optional:
  -dry-run         Preview actions without writing files
```

## Examples

The `examples/` directory contains a complete working example:

```bash
# Preview the example
copycat -model examples/model.yaml -template examples/template -out ./output -dry-run

# Generate actual files
copycat -model examples/model.yaml -template examples/template -out ./output
```

Or run directly from source:

```bash
# Preview the example
go run cmd/copycat/main.go -model examples/model.yaml -template examples/template -out ./output -dry-run

# Generate actual files
go run cmd/copycat/main.go -model examples/model.yaml -template examples/template -out ./output
```

## Template Features

### Array Iteration

When a path contains `{{ array.field }}`, copycat creates separate outputs for each array element:

```yaml
# model.yaml
features:
  - name: auth
    table: users
  - name: billing
    table: invoices
```

```
template/{{ features.name }}/{{ name }}.go.tmpl
```

Generates:
- `auth/auth.go`
- `billing/billing.go`

### Smart Cleanup

- Files that render to empty content are not created. Pre-existing file will be removed
- Empty directories automatically removed
- Pre-existing directories and files are preserved

## Library Usage

Use copycat as a Go library:

```go
package main

import (
    "log"
    
    "github.com/quintans/copycat"
    "github.com/spf13/afero"
)

func main() {
    // Load model from YAML file
    model, err := copycat.LoadModel("model.yaml")
    if err != nil {
        log.Fatal(err)
    }
    
    // Create CopyCat instance
    cc, err := copycat.NewCopyCat(
        afero.NewOsFs(), // input filesystem
        afero.NewOsFs(), // output filesystem
        model,
    )
    if err != nil {
        log.Fatal(err)
    }
    
    // Run template expansion
    err = cc.Run("template", "output", false) // false = not dry-run
    if err != nil {
        log.Fatal(err)
    }
}
```

## Development

### Prerequisites

- Go 1.25.1 or later

### Running Tests

```bash
go test -v
```

### Dependencies

- `github.com/spf13/afero` - Filesystem abstraction
- `github.com/go-task/slim-sprig/v3` - Template functions
- `gopkg.in/yaml.v3` - YAML parsing
- `github.com/quintans/faults` - Error handling
