package forge

import (
	"context"
	"testing"
)

func TestParsePRBatch(t *testing.T) {
	branches := []string{"merged", "open", "none", "lower"}
	data := `{"data":{"repository":{
		"b0":{"nodes":[{"number":123,"state":"MERGED","headRefOid":"abc123","baseRefName":"main"}]},
		"b1":{"nodes":[{"number":7,"state":"OPEN","headRefOid":"def456","baseRefName":"develop"}]},
		"b2":{"nodes":[]},
		"b3":{"nodes":[{"number":9,"state":"merged","headRefOid":"aaa","baseRefName":"main"}]}
	}}}`

	prs := map[string]*PullRequest{}
	if err := parsePRBatch([]byte(data), branches, prs); err != nil {
		t.Fatalf("parsePRBatch: %v", err)
	}

	want := map[string]PullRequest{
		"merged": {Number: 123, State: StateMerged, HeadOID: "abc123", BaseRef: "main"},
		"open":   {Number: 7, State: StateOpen, HeadOID: "def456", BaseRef: "develop"},
		"lower":  {Number: 9, State: StateMerged, HeadOID: "aaa", BaseRef: "main"},
	}
	if len(prs) != len(want) {
		t.Errorf("got %d pull requests, want %d: %v", len(prs), len(want), prs)
	}
	for branch, w := range want {
		if got := prs[branch]; got == nil || *got != w {
			t.Errorf("prs[%q] = %+v, want %+v", branch, got, w)
		}
	}
}

func TestParsePRBatchErrors(t *testing.T) {
	for name, data := range map[string]string{
		"malformed":     `[]`,
		"no repository": `{"data":{"repository":null}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := parsePRBatch([]byte(data), []string{"a"}, map[string]*PullRequest{}); err == nil {
				t.Error("parsePRBatch: expected error, got nil")
			}
		})
	}
}

func TestPRQuery(t *testing.T) {
	want := "query($owner:String!,$name:String!,$b0:String!,$b1:String!){repository(owner:$owner,name:$name){" +
		"b0:pullRequests(headRefName:$b0,first:1,orderBy:{field:CREATED_AT,direction:DESC}){nodes{number state headRefOid baseRefName}}" +
		"b1:pullRequests(headRefName:$b1,first:1,orderBy:{field:CREATED_AT,direction:DESC}){nodes{number state headRefOid baseRefName}}" +
		"}}"
	if got := prQuery(2); got != want {
		t.Errorf("prQuery(2) =\n%s\nwant\n%s", got, want)
	}
}

func TestParseGitHubRepo(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"git@github.com:bkildow/wt-cli.git", "bkildow/wt-cli"},
		{"git@github.com:bkildow/wt-cli", "bkildow/wt-cli"},
		{"https://github.com/bkildow/wt-cli.git", "bkildow/wt-cli"},
		{"https://github.com/bkildow/wt-cli", "bkildow/wt-cli"},
		{"https://user@github.com/bkildow/wt-cli.git", "bkildow/wt-cli"},
		{"ssh://git@github.com/bkildow/wt-cli.git", "bkildow/wt-cli"},
		{"  https://github.com/bkildow/wt-cli/  ", "bkildow/wt-cli"},
		{"git@gitlab.com:bkildow/wt-cli.git", ""},
		{"https://bitbucket.org/bkildow/wt-cli.git", ""},
		{"/local/path/to/repo.git", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseGitHubRepo(tt.input); got != tt.want {
				t.Errorf("parseGitHubRepo(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectNonGitHubRemoteReturnsNil(t *testing.T) {
	if f := Detect(context.Background(), "git@gitlab.com:bkildow/wt-cli.git"); f != nil {
		t.Errorf("Detect(gitlab remote) = %v, want nil", f)
	}
	if f := Detect(context.Background(), ""); f != nil {
		t.Errorf("Detect(empty remote) = %v, want nil", f)
	}
}
