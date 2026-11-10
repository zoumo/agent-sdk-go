package agent_test

import (
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var executionOptionSurfaceSeparators = strings.NewReplacer("_", "", "-", "")

func TestExecutionOptionSurfaceNormalizationCoversSeparators(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "pascal_case", input: "PermissionMode", want: "permissionmode"},
		{name: "snake_case", input: "permission_mode", want: "permissionmode"},
		{name: "kebab_case", input: "permission-mode", want: "permissionmode"},
		{name: "fast_snake_case", input: "fast_mode", want: "fastmode"},
		{name: "custom_agent", input: "CustomAgent", want: "customagent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(
				t,
				test.want,
				normalizeExecutionOptionSurfaceValue(test.input),
			)
		})
	}
}

func TestExecutionOptionImplementationFilesAvoidUnsupportedProviderSettings(t *testing.T) {
	root := executionOptionsSourceRoot(t)
	files := executionOptionImplementationFiles(t, root)
	require.NotEmpty(t, files)
	assertExecutionOptionPackageConstants(t, root)
	relevant := 0
	for _, path := range files {
		parsed := parseExecutionOptionsSource(t, path)
		assertOnlySupportedExecutionOptionConstants(t, path, parsed)
		if !executionOptionSurfaceFile(path, parsed) {
			continue
		}
		relevant++
		relative, err := filepath.Rel(root, path)
		require.NoError(t, err)
		t.Run(filepath.ToSlash(relative), func(t *testing.T) {
			assertNoUnsupportedExecutionOptionSurface(t, path, parsed)
		})
	}
	assert.Positive(t, relevant, "no execution-option surface files found")
}

func executionOptionsSourceRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	return filepath.Dir(file)
}

func executionOptionImplementationFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() ||
			!strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	require.NoError(t, err)
	slices.Sort(files)
	return files
}

func parseExecutionOptionsSource(t *testing.T, path string) *ast.File {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	return parsed
}

func executionOptionSurfaceFile(path string, file *ast.File) bool {
	if strings.HasPrefix(filepath.Base(path), "execution_options") {
		return true
	}
	relevant := false
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.GenDecl)
		if !ok {
			return true
		}
		for _, specification := range declaration.Specs {
			switch value := specification.(type) {
			case *ast.TypeSpec:
				if executionOptionSurfaceName(value.Name.Name) {
					relevant = true
					return false
				}
			case *ast.ValueSpec:
				for _, name := range value.Names {
					if executionOptionSurfaceName(name.Name) {
						relevant = true
						return false
					}
				}
			}
		}
		return true
	})
	return relevant
}

func executionOptionSurfaceName(name string) bool {
	return strings.HasPrefix(name, "ExecutionOption") ||
		name == "OpenOptions" ||
		name == "ResumeOptions"
}

func assertNoUnsupportedExecutionOptionSurface(
	t *testing.T,
	path string,
	file *ast.File,
) {
	t.Helper()
	bannedFragments := []string{
		"sandbox",
		"approval",
		"permission",
		"collaboration",
		"approvalpolicy",
		"permissionsprofile",
		"collaborationmode",
		"fastmode",
		"permissionmode",
		"customagent",
		"acpmode",
	}
	bannedValues := []string{"agent", "fast", "mode"}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.BasicLit:
			if value.Kind != token.STRING {
				return true
			}
			decoded, err := strconv.Unquote(value.Value)
			require.NoError(t, err)
			assertUnsupportedValueAbsent(
				t,
				decoded,
				bannedFragments,
				bannedValues,
			)
		case *ast.StructType:
			for _, field := range value.Fields.List {
				for _, name := range field.Names {
					assertUnsupportedValueAbsent(
						t,
						name.Name,
						bannedFragments,
						bannedValues,
					)
				}
			}
		case *ast.Ident:
			assertUnsupportedValueAbsent(
				t,
				value.Name,
				bannedFragments,
				nil,
			)
		}
		return true
	})
}

func assertOnlySupportedExecutionOptionConstants(
	t *testing.T,
	path string,
	file *ast.File,
) {
	t.Helper()
	allowed := map[string]string{
		"ExecutionOptionModel":           "model",
		"ExecutionOptionReasoningEffort": "reasoningEffort",
	}
	typeNames := executionOptionTypeNames(file)
	strictFile := strings.HasPrefix(filepath.Base(path), "execution_options")
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.GenDecl)
		if !ok {
			return true
		}
		if declaration.Tok == token.TYPE {
			for _, specification := range declaration.Specs {
				value, ok := specification.(*ast.TypeSpec)
				if !ok || !executionOptionTypeName(value.Type, typeNames) {
					continue
				}
				assert.Failf(
					t,
					"execution option alias",
					"%s: type %s aliases or derives from ExecutionOption",
					path,
					value.Name.Name,
				)
			}
			return true
		}
		if declaration.Tok != token.CONST {
			return true
		}
		for _, specification := range declaration.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			isOption := strictFile || hasExecutionOptionType(values, typeNames)
			for index, name := range values.Names {
				if !isOption &&
					!strings.HasPrefix(name.Name, "ExecutionOption") {
					continue
				}
				want, supported := allowed[name.Name]
				if !assert.True(
					t,
					supported,
					"%s: unsupported execution option constant %s",
					path,
					name.Name,
				) {
					continue
				}
				if !assert.Len(
					t,
					values.Values,
					len(values.Names),
					"%s: execution option constant %s must use its own literal",
					path,
					name.Name,
				) {
					continue
				}
				literal, ok := values.Values[index].(*ast.BasicLit)
				if !assert.True(
					t,
					ok && literal.Kind == token.STRING,
					"%s: execution option constant %s must use a string literal",
					path,
					name.Name,
				) {
					continue
				}
				decoded, err := strconv.Unquote(literal.Value)
				require.NoError(t, err)
				assert.Equal(t, want, decoded, path)
			}
		}
		return true
	})
}

func executionOptionTypeNames(file *ast.File) map[string]bool {
	names := map[string]bool{"ExecutionOption": true}
	for changed := true; changed; {
		changed = false
		ast.Inspect(file, func(node ast.Node) bool {
			specification, ok := node.(*ast.TypeSpec)
			if !ok || names[specification.Name.Name] {
				return true
			}
			if executionOptionTypeName(specification.Type, names) {
				names[specification.Name.Name] = true
				changed = true
			}
			return true
		})
	}
	return names
}

func executionOptionTypeName(expression ast.Expr, names map[string]bool) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		return names[value.Name]
	case *ast.SelectorExpr:
		return names[value.Sel.Name]
	case *ast.ParenExpr:
		return executionOptionTypeName(value.X, names)
	default:
		return false
	}
}

func hasExecutionOptionType(values *ast.ValueSpec, names map[string]bool) bool {
	if executionOptionTypeName(values.Type, names) {
		return true
	}
	for _, expression := range values.Values {
		call, ok := expression.(*ast.CallExpr)
		if !ok {
			continue
		}
		if executionOptionTypeName(call.Fun, names) {
			return true
		}
	}
	return false
}

func assertExecutionOptionPackageConstants(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(
			fset,
			filepath.Join(root, entry.Name()),
			nil,
			parser.SkipObjectResolution,
		)
		require.NoError(t, err)
		files = append(files, parsed)
	}
	require.NotEmpty(t, files)
	info := &types.Info{Defs: make(map[*ast.Ident]types.Object)}
	checked, err := (&types.Config{Importer: importer.Default()}).Check(
		"github.com/zoumo/agent-sdk-go/agent",
		fset,
		files,
		info,
	)
	require.NoError(t, err)
	executionOption := checked.Scope().Lookup("ExecutionOption")
	require.NotNil(t, executionOption)
	executionOptionType := executionOption.Type()
	allowed := map[string]string{
		"ExecutionOptionModel":           "model",
		"ExecutionOptionReasoningEffort": "reasoningEffort",
	}
	found := make(map[string]bool, len(allowed))
	for identifier, object := range info.Defs {
		position := fset.Position(identifier.Pos())
		switch value := object.(type) {
		case *types.Const:
			if !types.Identical(value.Type(), executionOptionType) {
				continue
			}
			want, ok := allowed[identifier.Name]
			if !assert.True(
				t,
				ok,
				"%s: unsupported ExecutionOption constant %s",
				position,
				identifier.Name,
			) {
				continue
			}
			found[identifier.Name] = true
			if !assert.Equal(
				t,
				constant.String,
				value.Val().Kind(),
				"%s: ExecutionOption constant %s must be a string",
				position,
				identifier.Name,
			) {
				continue
			}
			assert.Equal(
				t,
				want,
				constant.StringVal(value.Val()),
				position,
			)
		case *types.TypeName:
			if identifier.Name == "ExecutionOption" {
				continue
			}
			assert.False(
				t,
				types.Identical(types.Unalias(value.Type()), executionOptionType),
				"%s: type %s aliases ExecutionOption",
				position,
				identifier.Name,
			)
		}
	}
	for name := range allowed {
		assert.True(t, found[name], "missing ExecutionOption constant %s", name)
	}
}

func assertUnsupportedValueAbsent(
	t *testing.T,
	value string,
	bannedFragments []string,
	bannedValues []string,
) {
	t.Helper()
	normalized := normalizeExecutionOptionSurfaceValue(value)
	for _, fragment := range bannedFragments {
		assert.NotContains(
			t,
			normalized,
			normalizeExecutionOptionSurfaceValue(fragment),
		)
	}
	assert.False(t, slices.Contains(bannedValues, normalized), value)
}

func normalizeExecutionOptionSurfaceValue(value string) string {
	return strings.ToLower(executionOptionSurfaceSeparators.Replace(value))
}
