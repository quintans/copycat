package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessDirWithExamples(t *testing.T) {
	// Load the actual model from examples
	model, err := loadYAMLModel("examples/model.yaml")
	require.NoError(t, err, "failed to load model")

	// Create in-memory filesystem to capture outputs
	memFS := make(fstest.MapFS)

	// Test function that captures file writes to our in-memory FS
	var capturedFiles = make(map[string]string)
	var capturedDirs = make(map[string]bool)

	// Override file operations for testing by using a custom processDir
	err = processTestDir(t, "examples/template", "", memFS, "", model, model, capturedFiles, capturedDirs)
	require.NoError(t, err, "processTestDir should not fail")

	// Verify expected directory structure
	expectedDirs := []string{
		"MyApp",
		"MyApp/auth",
		"MyApp/payments",
	}

	for _, dir := range expectedDirs {
		assert.True(t, capturedDirs[dir], "expected directory %s was not created", dir)
	}

	// Verify expected files and their content
	expectedFiles := map[string]struct {
		shouldContain    []string
		shouldNotContain []string
	}{
		"MyApp/README.md": {
			shouldContain: []string{"MyApp", "auth", "payments"},
		},
		"MyApp/auth/config.txt": {
			shouldContain: []string{"Feature: auth", "Project: MyApp", "Owner: Alice"},
		},
		"MyApp/auth/auth.go": {
			shouldContain: []string{"package auth", "Auto-generated for feature auth", "MyApp", `return "auths"`},
		},
		"MyApp/payments/config.txt": {
			shouldContain: []string{"Feature: payments", "Project: MyApp", "Owner: Alice"},
		},
		"MyApp/payments/payments.go": {
			shouldContain: []string{"package payments", "Auto-generated for feature payments", "MyApp", `return "payments"`},
		},
	}

	for filePath, expected := range expectedFiles {
		content, exists := capturedFiles[filePath]
		if !assert.True(t, exists, "expected file %s was not created", filePath) {
			continue
		}

		for _, shouldContain := range expected.shouldContain {
			assert.Contains(t, content, shouldContain, "file %s should contain %q", filePath, shouldContain)
		}

		for _, shouldNotContain := range expected.shouldNotContain {
			assert.NotContains(t, content, shouldNotContain, "file %s should not contain %q", filePath, shouldNotContain)
		}
	}
}

func TestExpandPathSegmentScalar(t *testing.T) {
	model := map[string]any{
		"projectName": "TestProject",
	}

	segments, err := expandPathSegment("{{ projectName }}", model, model)
	require.NoError(t, err, "expandPathSegment should not fail")
	require.Len(t, segments, 1, "should have exactly 1 segment")

	assert.Equal(t, "TestProject", segments[0].Rendered)
}

func TestExpandPathSegmentArray(t *testing.T) {
	model := map[string]any{
		"features": []any{
			map[string]any{"name": "users", "table": "users"},
			map[string]any{"name": "orders", "table": "orders"},
		},
	}

	segments, err := expandPathSegment("{{ features.name }}", model, model)
	require.NoError(t, err, "expandPathSegment should not fail")
	require.Len(t, segments, 2, "should have exactly 2 segments")

	expectedNames := []string{"users", "orders"}
	for i, seg := range segments {
		assert.Equal(t, expectedNames[i], seg.Rendered, "segment %d rendering", i)

		// Verify context is the feature element
		ctx, ok := seg.Ctx.(map[string]any)
		assert.True(t, ok, "segment %d: context should be map[string]any, got %T", i, seg.Ctx)
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

	rendered, err := renderContent(template, rootModel, featureCtx)
	require.NoError(t, err, "renderContent should not fail")

	expected := `package auth

// Auto-generated for feature auth in project TestApp

func TableName() string { return "auths" }`

	assert.Equal(t, expected, rendered)
}

func TestEvaluatePathNodes(t *testing.T) {
	model := map[string]any{
		"features": []any{
			map[string]any{"name": "auth", "table": "auths"},
			map[string]any{"name": "payments", "table": "payments"},
		},
	}

	// Test broadcasting over array
	nodes, ok := evaluatePathNodes(model, "features.name")
	require.True(t, ok, "evaluatePathNodes should have succeeded")
	require.Len(t, nodes, 2, "should have exactly 2 nodes")

	expectedNames := []string{"auth", "payments"}
	for i, node := range nodes {
		assert.Equal(t, expectedNames[i], node.Value, "node %d value", i)

		// Context should be the feature element
		ctx, ok := node.Ctx.(map[string]any)
		assert.True(t, ok, "node %d: context should be map[string]any, got %T", i, node.Ctx)
		if ok {
			assert.Equal(t, expectedNames[i], ctx["name"], "node %d: context name", i)
		}
	}
}

func TestNormalize(t *testing.T) {
	// Test YAML map[any]any to map[string]any conversion
	input := map[any]any{
		"projectName": "TestApp",
		123:           "numeric key",
		"nested": map[any]any{
			"inner": "value",
		},
		"array": []any{
			map[any]any{"name": "item1"},
			"simple",
		},
	}

	result := normalize(input)

	resultMap, ok := result.(map[string]any)
	require.True(t, ok, "normalize should return map[string]any, got %T", result)

	assert.Equal(t, "TestApp", resultMap["projectName"])
	assert.Equal(t, "numeric key", resultMap["123"])

	// Check nested map normalization
	nested, ok := resultMap["nested"].(map[string]any)
	assert.True(t, ok, "expected nested to be map[string]any, got %T", resultMap["nested"])
	if ok {
		assert.Equal(t, "value", nested["inner"])
	}
}

// processTestDir is a test version of processDir that captures operations in memory
func processTestDir(t *testing.T, templateRoot string, srcRel string, memFS fstest.MapFS, outRel string, rootModel map[string]any, ctx any, capturedFiles map[string]string, capturedDirs map[string]bool) error {
	currentTemplatePath := filepath.Join(templateRoot, srcRel)

	entries, err := os.ReadDir(currentTemplatePath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		expanded, err := expandPathSegment(name, rootModel, ctx)
		if err != nil {
			return err
		}

		for _, seg := range expanded {
			srcPath := filepath.Join(templateRoot, srcRel, name)

			if entry.IsDir() {
				newOutRel := filepath.Join(outRel, seg.Rendered)
				// Normalize path separators for consistent testing
				normalizedPath := filepath.ToSlash(newOutRel)
				capturedDirs[normalizedPath] = true

				// Recurse into directory
				if err := processTestDir(t, templateRoot, filepath.Join(srcRel, name), memFS, newOutRel, rootModel, seg.Ctx, capturedFiles, capturedDirs); err != nil {
					return err
				}
			} else {
				// File: render content
				data, err := os.ReadFile(srcPath)
				if err != nil {
					return err
				}

				rendered, err := renderContent(string(data), rootModel, seg.Ctx)
				if err != nil {
					return err
				}

				outName := seg.Rendered
				if strings.HasSuffix(name, ".tmpl") {
					outName = strings.TrimSuffix(outName, ".tmpl")
				}

				// Skip empty files (same logic as main implementation)
				if strings.TrimSpace(rendered) == "" {
					continue
				}

				newOutRel := filepath.Join(outRel, outName)
				// Normalize path separators for consistent testing
				normalizedPath := filepath.ToSlash(newOutRel)
				capturedFiles[normalizedPath] = rendered
			}
		}
	}
	return nil
}

func TestCompleteTemplateExpansion(t *testing.T) {
	// Test with a more complex model to verify all edge cases
	complexModel := map[string]any{
		"projectName": "ComplexApp",
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

	// Create a simple in-memory template structure for testing
	memFS := make(fstest.MapFS)
	capturedFiles := make(map[string]string)
	capturedDirs := make(map[string]bool)

	// Test the actual examples
	err := processTestDir(t, "examples/template", "", memFS, "", complexModel, complexModel, capturedFiles, capturedDirs)
	require.NoError(t, err, "processTestDir should not fail")

	// Should create directories for each feature
	expectedDirs := []string{
		"ComplexApp",
		"ComplexApp/authentication",
		"ComplexApp/billing",
	}

	for _, dir := range expectedDirs {
		assert.True(t, capturedDirs[dir], "expected directory %s was not created", dir)
	}

	// Should create files with proper context
	if content, exists := capturedFiles["ComplexApp/authentication/authentication.go"]; assert.True(t, exists, "ComplexApp/authentication/authentication.go was not created") {
		assert.Contains(t, content, "package authentication", "authentication.go should contain package declaration")
		assert.Contains(t, content, `return "auth_users"`, "authentication.go should contain table name")
	}

	if content, exists := capturedFiles["ComplexApp/billing/billing.go"]; assert.True(t, exists, "ComplexApp/billing/billing.go was not created") {
		assert.Contains(t, content, "package billing", "billing.go should contain package declaration")
		assert.Contains(t, content, `return "invoices"`, "billing.go should contain table name")
	}
}

func TestEmptyArrayHandling(t *testing.T) {
	model := map[string]any{
		"projectName": "EmptyTest",
		"features":    []any{}, // Empty array
	}

	// Test expansion with empty array - should produce no output (not an error)
	segments, err := expandPathSegment("{{ features.name }}", model, model)
	require.NoError(t, err, "expandPathSegment should handle empty arrays gracefully")
	assert.Empty(t, segments, "empty array should produce no segments")
}

func TestMissingFieldHandling(t *testing.T) {
	model := map[string]any{
		"projectName": "TestApp",
	}

	// Test accessing non-existent field - should fall back to template evaluation
	_, err := expandPathSegment("{{ nonexistent }}", model, model)
	require.NoError(t, err, "expandPathSegment should not fail on missing field")
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
	nodes, ok := evaluatePathNodes(model, "features.nested.value")
	require.True(t, ok, "evaluatePathNodes should succeed for nested path")
	require.Len(t, nodes, 1, "should have exactly 1 node")

	assert.Equal(t, "deep-value", nodes[0].Value)
}

func TestToStringConversions(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
		hasError bool
	}{
		{"string", "hello", "hello", false},
		{"int", 42, "42", false},
		{"float", 3.14, "3.14", false},
		{"bool true", true, "true", false},
		{"bool false", false, "false", false},
		{"nil", nil, "", false},
		{"map with name", map[string]any{"name": "test"}, "test", false},
		{"map without name", map[string]any{"other": "value"}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := toString(tt.input)

			if tt.hasError {
				assert.Error(t, err, "expected error but got none")
			} else {
				assert.NoError(t, err, "unexpected error")
				assert.Equal(t, tt.expected, result)
			}
		})
	}
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
	template := "Project: {{ (root).projectName }}, Feature: {{ .name }}"
	rendered, err := renderContent(template, rootModel, ctx)
	require.NoError(t, err, "renderContent should not fail")

	expected := "Project: HelperTest, Feature: feature1"
	assert.Equal(t, expected, rendered)
}

func TestDryRunMode(t *testing.T) {
	// This is more of an integration test to ensure dry-run doesn't write files
	// We'd need to modify the main function to be more testable, but for now
	// we can test the core logic components

	model := map[string]any{
		"projectName": "DryRunTest",
		"features": []any{
			map[string]any{"name": "test1"},
		},
	}

	// Test that our core functions work without side effects
	segments, err := expandPathSegment("{{ projectName }}", model, model)
	require.NoError(t, err, "expandPathSegment should not fail")

	assert.Len(t, segments, 1, "should have exactly 1 segment")
	assert.Equal(t, "DryRunTest", segments[0].Rendered)
}

func TestEmptyArrayIntegration(t *testing.T) {
	// Test complete directory processing with empty arrays
	emptyModel := map[string]any{
		"projectName": "EmptyProject",
		"features":    []any{}, // Empty array - should create no feature directories
		"owner":       map[string]any{"name": "TestUser"},
	}

	memFS := make(fstest.MapFS)
	capturedFiles := make(map[string]string)
	capturedDirs := make(map[string]bool)

	// Process the examples template with empty features array
	err := processTestDir(t, "examples/template", "", memFS, "", emptyModel, emptyModel, capturedFiles, capturedDirs)
	require.NoError(t, err, "processTestDir should handle empty arrays gracefully")

	// Should create the project directory but no feature directories
	assert.True(t, capturedDirs["EmptyProject"], "project directory should be created")
	assert.False(t, capturedDirs["EmptyProject/auth"], "no auth directory should be created")
	assert.False(t, capturedDirs["EmptyProject/payments"], "no payments directory should be created")

	// Should create the README.md but no feature-specific files
	assert.Contains(t, capturedFiles, "EmptyProject/README.md", "project README should be created")
	assert.NotContains(t, capturedFiles, "EmptyProject/auth/auth.go", "no auth files should be created")
	assert.NotContains(t, capturedFiles, "EmptyProject/payments/payments.go", "no payment files should be created")

	// The README should still render correctly even with empty features
	readmeContent := capturedFiles["EmptyProject/README.md"]
	assert.Contains(t, readmeContent, "EmptyProject", "README should contain project name")

	// Verify empty.txt is not created (it should be skipped as empty)
	assert.NotContains(t, capturedFiles, "EmptyProject/empty.txt", "empty files should not be created")
}

func TestEmptyFileHandling(t *testing.T) {
	// Create a test model and verify empty files are skipped
	model := map[string]any{
		"projectName": "TestEmpty",
		"features": []any{
			map[string]any{"name": "feature1", "table": "table1"},
		},
		"owner": map[string]any{
			"name": "TestOwner",
		},
	}

	memFS := make(fstest.MapFS)
	capturedFiles := make(map[string]string)
	capturedDirs := make(map[string]bool)

	err := processTestDir(t, "examples/template", "", memFS, "", model, model, capturedFiles, capturedDirs)
	require.NoError(t, err, "processTestDir should not fail")

	// Verify that empty.txt is not in the captured files (should be skipped)
	assert.NotContains(t, capturedFiles, "TestEmpty/empty.txt", "empty files should be skipped")

	// But other files should still be created
	assert.Contains(t, capturedFiles, "TestEmpty/README.md", "non-empty files should be created")
	assert.Contains(t, capturedFiles, "TestEmpty/feature1/feature1.go", "feature files should be created")
}

func TestRemoveEmptyDirs(t *testing.T) {
	// Test the removeEmptyCreatedDirs function directly
	// Create a temporary directory structure for testing
	tempDir := t.TempDir()

	// Create nested directories
	emptyDir := filepath.Join(tempDir, "empty")
	nonEmptyDir := filepath.Join(tempDir, "nonempty")
	nestedEmptyDir := filepath.Join(tempDir, "nested", "empty")
	preExistingEmpty := filepath.Join(tempDir, "preexisting", "empty")

	err := os.MkdirAll(emptyDir, 0o755)
	require.NoError(t, err)
	err = os.MkdirAll(nonEmptyDir, 0o755)
	require.NoError(t, err)
	err = os.MkdirAll(nestedEmptyDir, 0o755)
	require.NoError(t, err)
	err = os.MkdirAll(preExistingEmpty, 0o755)
	require.NoError(t, err)

	// Add a file to the non-empty directory
	err = os.WriteFile(filepath.Join(nonEmptyDir, "file.txt"), []byte("content"), 0o644)
	require.NoError(t, err)

	// Simulate tracking only some directories as "created by copycat"
	createdDirs := map[string]struct{}{
		emptyDir:                         {}, // This empty dir should be removed
		nonEmptyDir:                      {}, // This non-empty dir should remain
		nestedEmptyDir:                   {}, // This empty dir should be removed
		filepath.Join(tempDir, "nested"): {}, // Parent should also be removed when child is gone
		// Note: preExistingEmpty is NOT in createdDirs, so should not be touched
	}

	// Run removeEmptyCreatedDirs
	err = removeEmptyCreatedDirs(createdDirs)
	require.NoError(t, err)

	// Verify only created empty directories are removed
	_, err = os.Stat(emptyDir)
	assert.True(t, os.IsNotExist(err), "empty directory that was created should be removed")

	_, err = os.Stat(nestedEmptyDir)
	assert.True(t, os.IsNotExist(err), "nested empty directory that was created should be removed")

	_, err = os.Stat(filepath.Join(tempDir, "nested"))
	assert.True(t, os.IsNotExist(err), "parent of empty directory should also be removed when tracked")

	// Verify non-empty directory remains
	_, err = os.Stat(nonEmptyDir)
	assert.NoError(t, err, "non-empty directory should remain")

	_, err = os.Stat(filepath.Join(nonEmptyDir, "file.txt"))
	assert.NoError(t, err, "file in non-empty directory should remain")

	// Verify pre-existing empty directory is NOT removed
	_, err = os.Stat(preExistingEmpty)
	assert.NoError(t, err, "pre-existing empty directory should not be removed")
}

func TestPreExistingDirectoryPreservation(t *testing.T) {
	// Test that pre-existing directories are not removed even if empty
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "template")
	outputDir := filepath.Join(tempDir, "output")

	// Create pre-existing directory structure
	preExistingDir := filepath.Join(outputDir, "PreExisting")
	preExistingEmptyDir := filepath.Join(preExistingDir, "EmptySubdir")
	err := os.MkdirAll(preExistingEmptyDir, 0o755)
	require.NoError(t, err)

	// Add a file to show the parent isn't empty
	err = os.WriteFile(filepath.Join(preExistingDir, "existing.txt"), []byte("pre-existing content"), 0o644)
	require.NoError(t, err)

	// Create a simple template that creates some directories
	err = os.MkdirAll(filepath.Join(templateDir, "{{ projectName }}", "newdir"), 0o755)
	require.NoError(t, err)

	// Create template files - one that produces content, one that's empty
	err = os.WriteFile(filepath.Join(templateDir, "{{ projectName }}", "README.md"), []byte("# {{ .projectName }}"), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(templateDir, "{{ projectName }}", "newdir", "empty.txt.tmpl"), []byte(""), 0o644)
	require.NoError(t, err)

	// Run copycat
	model := map[string]any{"projectName": "TestProject"}
	createdDirs := make(map[string]struct{})
	err = processDir(templateDir, outputDir, model, model, false, createdDirs)
	require.NoError(t, err)

	err = removeEmptyCreatedDirs(createdDirs)
	require.NoError(t, err)

	// Verify results:
	// 1. Pre-existing directories should remain
	_, err = os.Stat(preExistingDir)
	assert.NoError(t, err, "pre-existing directory should remain")

	_, err = os.Stat(preExistingEmptyDir)
	assert.NoError(t, err, "pre-existing empty subdirectory should remain")

	_, err = os.Stat(filepath.Join(preExistingDir, "existing.txt"))
	assert.NoError(t, err, "pre-existing file should remain")

	// 2. New project directory should be created
	_, err = os.Stat(filepath.Join(outputDir, "TestProject"))
	assert.NoError(t, err, "new project directory should be created")

	_, err = os.Stat(filepath.Join(outputDir, "TestProject", "README.md"))
	assert.NoError(t, err, "new project README should be created")

	// 3. Directory that would only contain empty files should be removed
	_, err = os.Stat(filepath.Join(outputDir, "TestProject", "newdir"))
	assert.True(t, os.IsNotExist(err), "directory with only empty files should be removed")
}

func TestEndToEndEmptyDirRemoval(t *testing.T) {
	// Test that combines empty file skipping and empty directory removal
	model := map[string]any{
		"projectName": "TestEmptyDirs",
		"features": []any{
			map[string]any{"name": "onlyEmptyFiles", "table": "empty"},
		},
		"owner": map[string]any{
			"name": "TestOwner",
		},
	}

	// Create a temporary template structure with only empty files in one feature
	tempDir := t.TempDir()
	templateDir := filepath.Join(tempDir, "template")

	// Create the template structure
	err := os.MkdirAll(filepath.Join(templateDir, "{{ projectName }}", "{{ features.name }}"), 0o755)
	require.NoError(t, err)

	// Create an empty template file
	err = os.WriteFile(filepath.Join(templateDir, "{{ projectName }}", "{{ features.name }}", "empty.txt.tmpl"), []byte(""), 0o644)
	require.NoError(t, err)

	// Create another empty template file
	err = os.WriteFile(filepath.Join(templateDir, "{{ projectName }}", "{{ features.name }}", "also_empty.go.tmpl"), []byte("{{/* comment only */}}"), 0o644)
	require.NoError(t, err)

	// Create project README (non-empty)
	err = os.WriteFile(filepath.Join(templateDir, "{{ projectName }}", "README.md"), []byte("# {{ .projectName }}"), 0o644)
	require.NoError(t, err)

	// Run copycat with this test template
	outputDir := filepath.Join(tempDir, "output")
	createdDirs := make(map[string]struct{})
	err = processDir(templateDir, outputDir, model, model, false, createdDirs)
	require.NoError(t, err)

	// Remove empty directories
	err = removeEmptyCreatedDirs(createdDirs)
	require.NoError(t, err) // Verify structure: project dir should exist but feature dir should be removed
	_, err = os.Stat(filepath.Join(outputDir, "TestEmptyDirs"))
	assert.NoError(t, err, "project directory should exist")

	_, err = os.Stat(filepath.Join(outputDir, "TestEmptyDirs", "README.md"))
	assert.NoError(t, err, "project README should exist")

	_, err = os.Stat(filepath.Join(outputDir, "TestEmptyDirs", "onlyEmptyFiles"))
	assert.True(t, os.IsNotExist(err), "directory with only empty files should be removed")
}
