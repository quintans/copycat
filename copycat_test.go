package copycat

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var customFuncs = map[string]any{
	"slugify": func(s string) string {
		// Simple slugify implementation: lower case and replace spaces with underscores
		return strings.ReplaceAll(strings.ToLower(s), " ", "_")
	},
}

func TestProcessDirWithExamples(t *testing.T) {
	// Load the actual model from examples
	model, err := LoadModel("examples/model.yaml")
	require.NoError(t, err, "failed to load model")

	// Create in-memory filesystem to capture outputs
	outFS := afero.NewMemMapFs()

	// Add a file to show its removal
	err = afero.WriteFile(outFS, "my_app/empty.txt", []byte("pre-existing content"), 0o644)
	require.NoError(t, err)

	cc, err := NewCopyCat(
		afero.NewOsFs(),
		outFS,
		model,
		WithCustomFuncs(customFuncs),
	)
	require.NoError(t, err)

	err = cc.Run("examples/template", "", false)
	require.NoError(t, err, "processDir should not fail")

	// Verify expected directory structure
	expectedDirs := []string{
		"my_app",
		"my_app/auth",
		"my_app/payments",
	}

	for _, dir := range expectedDirs {
		info, err := outFS.Stat(dir)
		require.NoError(t, err, "expected directory %s was not created", dir)
		assert.True(t, info.IsDir(), "expected %s to be a directory", dir)
	}

	// Verify expected files and their content
	expectedFiles := map[string]struct {
		shouldContain []string
	}{
		"my_app/README.md": {
			shouldContain: []string{
				"My App",
				"auth",
				"payments",
			},
		},
		"my_app/auth/config.txt": {
			shouldContain: []string{
				"Feature: auth",
				"Project: My App",
				"Slug: my_app",
				"Owner: Alice",
			},
		},
		"my_app/auth/auth.go": {
			shouldContain: []string{
				"package auth",
				"Auto-generated for feature auth",
				"My App",
				`return "auths"`,
			},
		},
		"my_app/payments/config.txt": {
			shouldContain: []string{
				"Feature: payments",
				"Project: My App",
				"Slug: my_app",
				"Owner: Alice",
			},
		},
		"my_app/payments/payments.go": {
			shouldContain: []string{
				"package payments",
				"Auto-generated for feature payments",
				"My App",
				`return "payments"`,
			},
		},
	}

	for filePath, expected := range expectedFiles {
		info, err := outFS.Stat(filePath)
		require.NoError(t, err, "expected file %s was not created", filePath)
		require.False(t, info.IsDir(), "expected %s to be a file", filePath)

		data, err := afero.ReadFile(outFS, filePath)
		require.NoError(t, err, "failed to read file %s", filePath)
		content := string(data)

		// Check that the file contains all expected substrings
		for _, shouldContain := range expected.shouldContain {
			assert.Contains(t, content, shouldContain, "file %s should contain %q", filePath, shouldContain)
		}
	}

	// Verify no unexpected files or directories were created
	// empty.txt should not be created as it renders to empty
	// db.go should not be created as hasDb is false, and consequently neither the gateway folder
	err = afero.Walk(outFS, "my_app", func(path string, info fs.FileInfo, err error) error {
		require.NoError(t, err, "error walking path %s", path)
		relPath, err := filepath.Rel("my_app", path)
		require.NoError(t, err, "error getting relative path for %s", path)
		if relPath == "." {
			return nil // Skip root
		}
		if info.IsDir() {
			assert.Contains(t, expectedDirs, path, "unexpected directory created: %s", path)
		} else {
			assert.Contains(t, expectedFiles, path, "unexpected file created: %s", path)
		}
		return nil
	})
	require.NoError(t, err, "error walking the output filesystem")
}

func TestExpandPathScalar(t *testing.T) {
	model := map[string]any{
		"projectName": "TestProject",
	}

	segments, err := expandPath("{{ projectName }}", model)
	require.NoError(t, err, "expandPath should not fail")
	require.Len(t, segments, 1, "should have exactly 1 segment")

	assert.Equal(t, "TestProject", segments[0].value, "expanded path should match")
}

func TestExpandPathSegmentArray(t *testing.T) {
	model := map[string]any{
		"features": []any{
			map[string]any{"name": "users", "table": "users"},
			map[string]any{"name": "orders", "table": "orders"},
		},
	}

	segments, err := expandPath("{{ features.name }}", model)
	require.NoError(t, err, "expandPath should not fail")
	require.Len(t, segments, 2, "should have exactly 2 segments")

	expectedNames := []string{"users", "orders"}
	for i, seg := range segments {
		assert.Equal(t, expectedNames[i], seg.value, "segment %d rendering", i)

		// Verify context is the feature element
		ctx, ok := seg.ctx.(map[string]any)
		assert.True(t, ok, "segment %d: context should be map[string]any, got %T", i, seg.ctx)
		if ok {
			assert.Equal(t, expectedNames[i], ctx["name"], "segment %d: context name", i)
		}
	}
}

func TestRenderContentWithContext(t *testing.T) {
	rootModel := map[string]any{
		"projectName": "TestApp",
		"owner":       map[string]any{"name": "Bob"},
	}

	featureCtx := map[string]any{
		"name":  "auth",
		"table": "auths",
	}

	template := `package {{ .name }}

// Auto-generated for feature {{ .name }} in project {{ (root).projectName }}

func TableName() string { return "{{ .table }}" }`

	cc := CopyCat{
		model:       rootModel,
		customFuncs: customFuncs,
	}
	rendered, err := cc.renderContent(template, featureCtx)
	require.NoError(t, err, "renderContent should not fail")

	expected := `package auth

// Auto-generated for feature auth in project TestApp

func TableName() string { return "auths" }`

	assert.Equal(t, expected, rendered)
}

func TestCompleteTemplateExpansion(t *testing.T) {
	// Test with a more complex model to verify all edge cases
	complexModel := map[string]any{
		"projectName": "Complex App",
		"projectSlug": `{{ lower .projectName | replace " " "_" }}`,
		"hasDb":       true,
		"version":     "1.0.0",
		"features": []any{
			map[string]any{
				"name":    "authentication",
				"table":   "auth_users",
				"enabled": true,
			},
			map[string]any{
				"name":    "billing",
				"table":   "invoices",
				"enabled": false,
			},
		},
		"owner": map[string]any{
			"name":  "Charlie",
			"email": "charlie@example.com",
		},
	}

	// Create in-memory filesystem to capture outputs
	outFS := afero.NewMemMapFs()

	cc, err := NewCopyCat(
		afero.NewOsFs(),
		outFS,
		complexModel,
	)
	require.NoError(t, err)

	err = cc.Run("examples/template", "", false)
	require.NoError(t, err, "processDir should not fail")

	// Should create directories for each feature
	expectedDirs := []string{
		"complex_app",
		"complex_app/authentication",
		"complex_app/billing",
		"complex_app/gateway", // because hasDb is true
	}

	for _, dir := range expectedDirs {
		_, err := outFS.Stat(dir)
		require.NoError(t, err, "expected directory %s was not created", dir)
	}

	data, err := afero.ReadFile(outFS, "complex_app/authentication/authentication.go")
	require.NoError(t, err, "failed to read authentication.go")
	content := string(data)
	assert.Contains(t, content, "package authentication", "authentication.go should contain package declaration")
	assert.Contains(t, content, `return "auth_users"`, "authentication.go should contain table name")

	data, err = afero.ReadFile(outFS, "complex_app/billing/billing.go")
	require.NoError(t, err, "failed to read billing.go")
	content = string(data)
	assert.Contains(t, content, "package billing", "billing.go should contain package declaration")
	assert.Contains(t, content, `return "invoices"`, "billing.go should contain table name")

	data, err = afero.ReadFile(outFS, "complex_app/gateway/db.go")
	require.NoError(t, err, "failed to read db.go")
	content = string(data)
	assert.Contains(t, content, "package gateway", "db.go should contain package declaration")
}

func TestEmptyArrayHandling(t *testing.T) {
	model := map[string]any{
		"projectName": "EmptyTest",
		"features":    []any{}, // Empty array
	}

	// Test expansion with empty array - should produce no output (not an error)
	segments, err := expandPath("{{ features.name }}", model)
	require.NoError(t, err, "expandPath should handle empty arrays gracefully")
	assert.Empty(t, segments, "empty array should produce no segments")
}

func TestMissingFieldHandling(t *testing.T) {
	model := map[string]any{
		"projectName": "TestApp",
	}

	// Test accessing non-existent field - should fall back to template evaluation
	_, err := expandPath("{{ nonexistent }}", model)
	require.NoError(t, err, "expandPath should not fail on missing field")
}

func TestNestedContextAccess(t *testing.T) {
	model := map[string]any{
		"projectName": "NestedTest",
		"features": []any{
			map[string]any{
				"name": "feature1",
				"nested": map[string]any{
					"value": "deep-value",
				},
			},
		},
	}

	// Test that we can access nested fields within array context
	result, err := expandPath("{{ features.nested.value }}", model)
	require.NoError(t, err, "expandPath should not fail")
	require.Len(t, result, 1, "should have exactly 1 node")

	assert.Equal(t, "deep-value", result[0].value, "nested value should match")
}

func TestTemplateHelperFunctions(t *testing.T) {
	rootModel := map[string]any{
		"projectName": "HelperTest",
		"version":     "2.0",
	}

	ctx := map[string]any{
		"name":    "feature1",
		"enabled": true,
	}

	// Test root helper function
	template := "Project: {{ root.projectName }}, Feature: {{ .name }}"
	cc := CopyCat{
		model: rootModel,
	}
	rendered, err := cc.renderContent(template, ctx)
	require.NoError(t, err, "renderContent should not fail")

	expected := "Project: HelperTest, Feature: feature1"
	assert.Equal(t, expected, rendered)
}

func TestDryRunMode(t *testing.T) {
	// Load the actual model from examples
	model, err := LoadModel("examples/model.yaml")
	require.NoError(t, err, "failed to load model")

	outFS := afero.NewMemMapFs()
	cc, err := NewCopyCat(
		afero.NewOsFs(),
		outFS,
		model,
		WithCustomFuncs(customFuncs),
	)
	require.NoError(t, err)

	err = cc.Run("examples/template", "", true)
	require.NoError(t, err, "ProcessDir should not fail")

	// Check that no files were created
	files, err := afero.ReadDir(outFS, "")
	require.NoError(t, err, "reading root of outFS should not fail")
	assert.Empty(t, files, "no files should be created in dry-run mode")
}

func TestPreExistingDirectoryPreservation(t *testing.T) {
	// Test that pre-existing directories are not removed even if empty
	inFS := afero.NewMemMapFs()
	outFS := afero.NewMemMapFs()

	templateDir := "template"
	outputDir := "output"

	// Create pre-existing directory structure
	preExistingDir := filepath.Join(outputDir, "PreExisting")
	preExistingEmptyDir := filepath.Join(preExistingDir, "EmptySubdir")
	err := outFS.MkdirAll(preExistingEmptyDir, 0o755)
	require.NoError(t, err)

	// Add a file to show the parent isn't empty
	err = afero.WriteFile(outFS, filepath.Join(preExistingDir, "existing.txt"), []byte("pre-existing content"), 0o644)
	require.NoError(t, err)

	// Create a simple template that creates some directories
	err = inFS.MkdirAll(filepath.Join(templateDir, "{{ projectName }}", "newdir"), 0o755)
	require.NoError(t, err)

	// Create template files - one that produces content, one that's empty
	err = afero.WriteFile(inFS, filepath.Join(templateDir, "{{ projectName }}", "README.md"), []byte("# {{ .projectName }}"), 0o644)
	require.NoError(t, err)

	err = afero.WriteFile(inFS, filepath.Join(templateDir, "{{ projectName }}", "newdir", "empty.txt.tmpl"), []byte(""), 0o644)
	require.NoError(t, err)

	// Run copycat
	model := map[string]any{"projectName": "TestProject"}
	cc, err := NewCopyCat(
		inFS,
		outFS,
		model,
	)
	require.NoError(t, err)

	err = cc.Run(templateDir, outputDir, false)
	require.NoError(t, err)

	// Verify results:
	// 1. Pre-existing directories should remain
	_, err = outFS.Stat(preExistingDir)
	assert.NoError(t, err, "pre-existing directory should remain")

	_, err = outFS.Stat(preExistingEmptyDir)
	assert.NoError(t, err, "pre-existing empty subdirectory should remain")

	_, err = outFS.Stat(filepath.Join(preExistingDir, "existing.txt"))
	assert.NoError(t, err, "pre-existing file should remain")

	// 2. New project directory should be created
	_, err = outFS.Stat(filepath.Join(outputDir, "TestProject"))
	assert.NoError(t, err, "new project directory should be created")

	_, err = outFS.Stat(filepath.Join(outputDir, "TestProject", "README.md"))
	assert.NoError(t, err, "new project README should be created")

	// 3. Directory that would only contain empty files should be removed
	_, err = outFS.Stat(filepath.Join(outputDir, "TestProject", "newdir"))
	assert.True(t, os.IsNotExist(err), "directory with only empty files should be removed")
}

func TestCustomFuncsAndRenderModel(t *testing.T) {
	model := map[string]any{
		"projectName": "My App",
		"projectSlug": `{{ .projectName | slugify }}`,
	}

	cc := CopyCat{
		model:       model,
		customFuncs: customFuncs,
	}
	m, err := cc.renderModelValue(model, model)
	require.NoError(t, err, "renderModel should not fail")
	model = m.(map[string]any)

	assert.Equal(t, "My App", model["projectName"], "projectName should remain unchanged")
	assert.Equal(t, "my_app", model["projectSlug"], "projectSlug should be rendered correctly")
}
