package openclaw

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseCredentialsKeepsDeclaredNamesThatAreInPlace(t *testing.T) {
	output := []byte(`notice before the json
{"skills":[
  {"name":"a","eligible":true,"disabled":false,"primaryEnv":"VENICE_API_KEY","missing":{"env":[]}},
  {"name":"b","eligible":true,"disabled":false,"primaryEnv":"JIRA_API_TOKEN","requirements":{"env":["JIRA_URL","JIRA_EMAIL"]},"missing":{"env":["JIRA_EMAIL"]}},
  {"name":"c","eligible":false,"disabled":false,"primaryEnv":"NOTION_API_TOKEN","missing":{"env":["NOTION_API_TOKEN"]}},
  {"name":"d","eligible":true,"disabled":true,"primaryEnv":"OPENAI_API_KEY","missing":{"env":[]}},
  {"name":"e","eligible":true,"disabled":false,"primaryEnv":"not a name; rm -rf","missing":{"env":[]}},
  {"name":"f","eligible":true,"disabled":false,"primaryEnv":"VENICE_API_KEY","missing":{"env":[]}}
]}`)
	want := []string{"JIRA_API_TOKEN", "JIRA_URL", "VENICE_API_KEY"}
	if got := parseCredentials(output); !reflect.DeepEqual(got, want) {
		t.Errorf("parseCredentials = %v, want %v", got, want)
	}
}

func TestParseCredentialsTellsUnknownFromNone(t *testing.T) {
	if got := parseCredentials([]byte("no json here")); got != nil {
		t.Errorf("unreadable output gave %v, want nil", got)
	}
	if got := parseCredentials([]byte(`{"skills":[]}`)); got == nil || len(got) != 0 {
		t.Errorf("no skills gave %v, want an empty list", got)
	}
}

// The adapter must learn about credentials by asking OpenClaw, never by
// looking for itself. This reads the adapter's own source and fails if any of
// it names an environment file, or reads this process's environment anywhere
// but in the functions that only hand it on to a command they run.
// The two functions that may touch this process's environment. Each passes it
// whole to a command it starts (OpenClaw, and git) and reads nothing from it.
var handsEnvironmentToAChild = map[string]bool{"commandEnv": true, "git": true}

func TestInventoryNeverReadsTheEnvironmentOrAnEnvFile(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				switch found := node.(type) {
				case *ast.SelectorExpr:
					pkg, ok := found.X.(*ast.Ident)
					if !ok || pkg.Name != "os" {
						return true
					}
					switch found.Sel.Name {
					case "Environ", "Getenv", "LookupEnv", "ExpandEnv":
						if !handsEnvironmentToAChild[function.Name.Name] {
							t.Errorf("%s: %s reads the environment with os.%s; only a function that hands it to a command it runs may", file, function.Name.Name, found.Sel.Name)
						}
					}
				case *ast.BasicLit:
					if found.Kind == token.STRING && strings.Contains(found.Value, ".env") {
						t.Errorf("%s: %s names an environment file (%s); the adapter must not open one", file, function.Name.Name, found.Value)
					}
				}
				return true
			})
		}
	}
}
