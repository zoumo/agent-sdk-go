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

func TestSessionConfigSurfaceReplacesLegacyExecutionOptions(t *testing.T) {
	checked := checkAgentProductionPackage(t)
	scope := checked.Scope()

	configID := scope.Lookup("ConfigID")
	require.NotNil(t, configID)
	assert.Equal(t, types.String, configID.Type().Underlying().(*types.Basic).Kind())

	allowed := map[string]string{
		"ConfigModel":  "model",
		"ConfigEffort": "effort",
	}
	found := make(map[string]bool, len(allowed))
	for identifier, object := range checked.Info.Defs {
		if alias, ok := object.(*types.TypeName); ok && identifier.Name != "ConfigID" {
			assert.False(t, types.Identical(types.Unalias(alias.Type()), configID.Type()),
				"%s must not alias ConfigID", identifier.Name)
		}
		value, ok := object.(*types.Const)
		if !ok || !types.Identical(value.Type(), configID.Type()) {
			continue
		}
		want, supported := allowed[identifier.Name]
		require.True(t, supported, "unexpected ConfigID constant %s", identifier.Name)
		found[identifier.Name] = true
		assert.Equal(t, constant.String, value.Val().Kind())
		assert.Equal(t, want, constant.StringVal(value.Val()))
	}
	for name := range allowed {
		assert.True(t, found[name], "missing ConfigID constant %s", name)
	}

	controller := scope.Lookup("ConfigController")
	require.NotNil(t, controller)
	controllerInterface, ok := controller.Type().Underlying().(*types.Interface)
	require.True(t, ok)
	require.Equal(t, 1, controllerInterface.NumMethods())
	assert.Equal(t, "SetConfig", controllerInterface.Method(0).Name())
}

// Scan declarations rather than whole provider files: unrelated permission
// interactions and native startup configuration are not unified config keys.
func TestSessionConfigProductionSurfaceRecursivelyExcludesUnsupportedSettings(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(current)
	packages := map[string]int{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		packages[filepath.Dir(path)]++
		for _, declaration := range parsed.Decls {
			if !sessionConfigDeclaration(declaration) {
				continue
			}
			for _, violation := range sessionConfigSurfaceViolations(declaration) {
				assert.Fail(t, "unsupported unified configuration surface", "%s: %s", path, violation)
			}
		}
		return nil
	})
	require.NoError(t, err)
	for _, subdir := range []string{".", "codex", "claude"} {
		assert.Positive(t, packages[filepath.Join(root, subdir)], "production package %s was not scanned", subdir)
	}

	// Additional fields cannot silently expand the creation or update contract.
	checked := checkAgentProductionPackage(t)
	for name, want := range map[string][]string{
		"SessionConfig": {"Model", "Effort"},
		"ConfigChange":  {"ID", "Value"},
	} {
		object := checked.Scope().Lookup(name)
		require.NotNil(t, object)
		structure, ok := object.Type().Underlying().(*types.Struct)
		require.True(t, ok)
		var fields []string
		for field := range structure.Fields() {
			fields = append(fields, field.Name())
		}
		assert.Equal(t, want, fields, name)
	}
}

func sessionConfigDeclaration(node ast.Node) bool {
	relevant := false
	ast.Inspect(node, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && slices.Contains([]string{
			"SessionConfig", "ConfigID", "ConfigChange", "ConfigReceipt", "ConfigController",
			"ConfigModel", "ConfigEffort", "SetConfig", "OpenOptions", "ResumeOptions",
			"threadConfig", "openingConfiguration", "nativeOptions",
		}, identifier.Name) {
			relevant = true
		}
		return true
	})
	return relevant
}

func sessionConfigSurfaceViolations(node ast.Node) []string {
	normalize := func(value string) string {
		return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(value))
	}
	var violations []string
	configName := func(expression ast.Expr) string {
		switch value := expression.(type) {
		case *ast.Ident:
			return value.Name
		case *ast.SelectorExpr:
			return value.Sel.Name
		}
		return ""
	}
	// Recognize only ConfigID values and ConfigChange.ID, so unrelated native
	// protocol switches can continue to preserve arbitrary string variants.
	ids, changes := map[string]bool{}, map[string]bool{}
	if function, ok := node.(*ast.FuncDecl); ok {
		for _, fields := range []*ast.FieldList{function.Recv, function.Type.Params} {
			if fields == nil {
				continue
			}
			for _, field := range fields.List {
				for _, name := range field.Names {
					switch configName(field.Type) {
					case "ConfigID":
						ids[name.Name] = true
					case "ConfigChange":
						changes[name.Name] = true
					}
				}
			}
		}
	}
	ast.Inspect(node, func(node ast.Node) bool {
		var value string
		switch node := node.(type) {
		case *ast.Ident:
			value = node.Name
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				value, _ = strconv.Unquote(node.Value)
			}
		}
		normalized := normalize(value)
		for _, banned := range []string{
			"sandbox", "approval", "permission", "collaboration", "fastmode", "customagent", "acpmode",
		} {
			if strings.Contains(normalized, banned) {
				violations = append(violations, value)
			}
		}
		if slices.Contains([]string{"agent", "fast", "mode"}, normalized) {
			// Package qualifiers are unrelated to a configuration value.
			if _, identifier := node.(*ast.Ident); !identifier {
				violations = append(violations, value)
			}
		}
		// Guard unknown strings even when they have no banned keyword.
		if statement, ok := node.(*ast.SwitchStmt); ok {
			isID := false
			switch tag := statement.Tag.(type) {
			case *ast.Ident:
				isID = ids[tag.Name]
			case *ast.SelectorExpr:
				if receiver, ok := tag.X.(*ast.Ident); ok {
					isID = changes[receiver.Name] && tag.Sel.Name == "ID"
				}
			}
			if isID {
				for _, clause := range statement.Body.List {
					for _, option := range clause.(*ast.CaseClause).List {
						if slices.Contains([]string{"ConfigModel", "ConfigEffort"}, configName(option)) {
							continue
						}
						if literal, ok := option.(*ast.BasicLit); ok && literal.Kind == token.STRING {
							value, _ := strconv.Unquote(literal.Value)
							if value == "model" || value == "effort" {
								continue
							}
						}
						violations = append(violations, "unknown ConfigID switch case")
					}
				}
			}
		}
		if specification, ok := node.(*ast.TypeSpec); ok && specification.Name.Name != "ConfigID" && configName(specification.Type) == "ConfigID" {
			violations = append(violations, "extra ConfigID type: "+specification.Name.Name)
		}
		if values, ok := node.(*ast.ValueSpec); ok {
			isID := configName(values.Type) == "ConfigID"
			for _, value := range values.Values {
				isID = isID || slices.Contains([]string{"ConfigModel", "ConfigEffort"}, configName(value))
			}
			if isID {
				for _, name := range values.Names {
					if name.Name != "ConfigModel" && name.Name != "ConfigEffort" {
						violations = append(violations, "extra ConfigID: "+name.Name)
					}
				}
			}
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if configName(call.Fun) == "ConfigID" {
				violations = append(violations, "ConfigID conversion bypasses the closed ID set")
			}
		}
		return true
	})
	return violations
}

func TestSessionConfigSurfaceGuardRecognizesUnsupportedAdditions(t *testing.T) {
	for _, source := range []string{
		`type SessionConfig struct { Sandbox string }`,
		`type SessionConfig struct { PermissionsProfile string }`,
		`const ConfigOther ConfigID = "unrecognized"`,
		`type Other = agent.ConfigID`,
		`const Other = agent.ConfigModel`,
		`func (c ConfigChange) Validate() { switch c.ID { case ConfigModel, ConfigEffort, "unrecognized": } }`,
		`func validate(id ConfigID) { switch id { case "unrecognized": } }`,
		`func mapping(c SessionConfig) { native.FastMode = true }`,
		`func mapping(c SessionConfig) { native["approval_policy"] = "never" }`,
		`func mapping(c SessionConfig) { native["collaboration-mode"] = "agent" }`,
		`func mapping(c SessionConfig) { id := agent.ConfigID("unrecognized") }`,
	} {
		t.Run(source, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", "package fixture\n"+source, parser.SkipObjectResolution)
			require.NoError(t, err)
			require.True(t, sessionConfigDeclaration(file.Decls[0]))
			assert.NotEmpty(t, sessionConfigSurfaceViolations(file.Decls[0]))
		})
	}
}

func TestLegacyUnifiedConfigurationSurfaceIsRemoved(t *testing.T) {
	checked := checkAgentProductionPackage(t)
	for _, name := range []string{
		"ExecutionOptions",
		"ExecutionOption",
		"ExecutionOptionChange",
		"ExecutionOptionsReceipt",
		"ExecutionOptionsReport",
		"ExecutionValue",
		"ExecutionOptionsReporter",
		"ExecutionOptionsController",
		"ExecutionOptionsRuntime",
		"SetExecutionOption",
		"SetExecutionModel",
		"ModelController",
		"ConfigurationReceipt",
	} {
		assert.Nil(t, checked.Scope().Lookup(name), "%s must not remain in package agent", name)
	}
}

type checkedAgentPackage struct {
	*types.Package
	Info *types.Info
}

func checkAgentProductionPackage(t *testing.T) checkedAgentPackage {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Dir(current)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() ||
			filepath.Ext(entry.Name()) != ".go" ||
			len(entry.Name()) > len("_test.go") &&
				entry.Name()[len(entry.Name())-len("_test.go"):] == "_test.go" {
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
	return checkedAgentPackage{Package: checked, Info: info}
}
