# copycat

A small Go utility to copy a template folder into a new location, expanding folder and file names that contain `{{ ... }}` expressions based on a YAML model. File contents are also rendered using Go text/templates.

## Features

- Expand folder/file names using `{{ ... }}` expressions that reference your model.
- If an expression resolves to an array, the path segment is expanded into multiple entries (one per element), and that element becomes the context for inner paths and file contents.
- Works for any `{{ ... }}` expression from your YAML model (not just `features.name`).
- Supports scalar model fields like `projectName` for single-file/folder generation.
- Renders file contents with Go `text/template` and [sprig] functions.
- **Smart empty handling**: Files that render to empty content are automatically skipped, and empty directories are removed.
- **Empty array support**: Arrays with no elements result in no folders/files being created for that path segment.
- Dry-run mode prints what would be generated without writing.

## Install

Build the binary:

```
go build -o copycat .
```

## Usage

```
./copycat --model model.yaml --template template_dir --out out_dir [--dry-run]
```

- `--model`: Path to YAML model
- `--template`: Path to input template directory
- `--out`: Output directory to write generated files
- `--dry-run`: Only print planned operations

## Template rules

- Path segments may include `{{ ... }}`. Examples:
  - `cmd/{{ projectName }}.go.tmpl` → becomes `cmd/myproj.go`
  - `pkg/{{ features.name }}/index.go.tmpl` → expands to one folder per feature name
- If a segment’s expression evaluates to an array, we expand that segment into multiple entries; the element becomes the current context for nested segments and file contents.
- File contents are rendered via Go templates. The template dot (`.`) is the current context (the array element during expansions, or the root model when not inside an expanded segment).
- Helper functions available inside templates:
  - `root` → returns the root model (use fields like `{{ (root).projectName }}` or `{{ root.projectName }}`)
  - `.` → returns the current context
  - Plus the full [sprig] function set.

Tip: Suffix content templates with `.tmpl`; the tool will strip `.tmpl` in the output file name.

## Example

Model (`examples/model.yaml`):

```
projectName: demoapp
features:
  - name: users
    table: users
  - name: orders
    table: orders
```

Template (`examples/template2`):

- `README.txt`
- `pkg/{{ features.name }}/index.go.tmpl`

Run dry-run:

```
./copycat --model examples/model.yaml --template examples/template2 --out ./out --dry-run
```

Expected output:

- `MKDIR ./out/pkg/users`
- `WRITE ./out/pkg/users/index.go`
- `MKDIR ./out/pkg/orders`
- `WRITE ./out/pkg/orders/index.go`

Empty files will show as `SKIP filename (empty after rendering)` in dry-run mode.

Then run the actual generation (omit `--dry-run`).

## Notes

- The model and template directory are not known at compile-time; the tool loads them at runtime.
- Nested arrays in a single path segment create a cartesian expansion only within that segment; inner segments use the current element as context, not cartesian across unrelated parts of the model.
- **Empty content handling**: Files that render to only whitespace are automatically skipped. Directories that become empty (due to all files being skipped) are automatically removed.
- **Empty arrays**: When a path expression resolves to an empty array, no files or directories are created for that expansion branch.

[sprig]: https://github.com/Masterminds/sprig