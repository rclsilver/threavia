package mcpconfig

import "testing"

func TestDeclarationsKeepCredentialsInBackendReferences(t *testing.T) {
	good := Server{Name: "github", Transport: "http", URL: "https://example.test/mcp", Headers: map[string]string{"Authorization": "GITHUB_AUTHORIZATION"}, Tools: []string{"issues_read"}, ReadTools: []string{"issues_read"}}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []Server{good, good, good, good, good}
	tests[0].Headers = map[string]string{"Authorization": "Bearer secret"}
	tests[1].URL = "https://user:password@example.test/mcp"
	tests[2].URL = "https://example.test/mcp?token=secret"
	tests[3].ReadTools = []string{"unselected"}
	tests[4].Tools = []string{"*"}
	for i, v := range tests {
		if v.Validate() == nil {
			t.Errorf("unsafe declaration %d accepted", i)
		}
	}
}

func TestWritePreauthorizationIsSeparateExactAndOptIn(t *testing.T) {
	base := Server{Name: "dev", Transport: "http", URL: "https://example.test/mcp", Tools: []string{"read", "write"}, ReadTools: []string{"read"}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{{"unselected"}, {"*"}, {"read"}, {"write", "write"}} {
		v := base
		v.WriteTools = names
		if v.Validate() == nil {
			t.Fatalf("invalid write grant accepted: %v", names)
		}
	}
	base.WriteTools = []string{"write"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
}
