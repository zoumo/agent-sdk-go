package agent_test

import (
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
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
