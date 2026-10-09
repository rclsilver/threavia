package api_test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTheRepositoryOfASessionIsReadByItsBackend pins where a Session's working
// directory stands in git: read by the backend holding it, when someone looks,
// and only in that directory.
func TestTheRepositoryOfASessionIsReadByItsBackend(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}

	root := t.TempDir()
	repository := filepath.Join(root, "puppet")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "site.pp"), []byte("node default {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--initial-branch", "main"},
		{"add", "site.pp"},
		{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repository
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "draft.pp"), []byte("class draft {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	var directory struct {
		ID string `json:"id"`
	}
	c.mustDo(http.MethodPost, "/api/v1/projects/"+project+"/directories",
		map[string]any{"name": "puppet"}, &directory, http.StatusCreated)
	c.connectClaudeBackend(credential, t.TempDir(), root)

	session := c.startSession(project, backendID, directory.ID, "look at the manifests")
	type repositoryResponse struct {
		Directory string `json:"directory"`
		Tracked   bool   `json:"tracked"`
		Branch    string `json:"branch"`
		Upstream  string `json:"upstream"`
		Untracked int32  `json:"untracked"`
		Unstaged  int32  `json:"unstaged"`
	}
	// The directory is located when the Job starts; until then there is
	// nowhere to read.
	var repo repositoryResponse
	waitUntil(t, "the directory to be located", func() bool {
		return c.do(http.MethodGet, "/api/v1/sessions/"+session+"/repository", nil, &repo) == http.StatusOK
	})
	if !repo.Tracked || repo.Branch != "main" || repo.Directory != repository {
		t.Fatalf("repository = %+v, want main in %s", repo, repository)
	}
	if repo.Untracked != 1 || repo.Unstaged != 0 || repo.Upstream != "" {
		t.Fatalf("one untracked file and no upstream: got %+v", repo)
	}

	// A fetch with no remote to fetch from is no failure of the read.
	if status := c.do(http.MethodGet, "/api/v1/sessions/"+session+"/repository?fetch=true", nil, &repo); status != http.StatusOK {
		t.Fatalf("GET repository?fetch=true = %d: %s", status, c.lastBody)
	}

	// A Session with no working directory reads where the backend puts
	// unscoped work, which is no repository here.
	unscoped := c.startSession(project, backendID, "", "anything")
	if status := c.do(http.MethodGet, "/api/v1/sessions/"+unscoped+"/repository", nil, &repo); status != http.StatusOK || repo.Tracked {
		t.Fatalf("an unscoped session = %d %+v, want a directory outside git", status, repo)
	}

	// Someone else's Session is not found, as anything else of theirs.
	if status := c.do(http.MethodGet, "/api/v1/sessions/d0000000-0000-4000-8000-000000000000/repository", nil, nil); status != http.StatusNotFound {
		t.Fatalf("an unknown session = %d, want 404", status)
	}
}
