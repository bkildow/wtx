// Package forge provides optional pull-request awareness backed by external
// CLIs. Everything here degrades gracefully: when no forge is usable, Detect
// returns nil and callers simply go without PR information.
package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// PRState mirrors the states a forge reports for a pull request.
type PRState string

const (
	StateOpen   PRState = "OPEN"
	StateMerged PRState = "MERGED"
	StateClosed PRState = "CLOSED"
)

// PullRequest is the minimal slice of PR data wt needs today. HeadOID is the
// commit the forge last saw on the head branch and BaseRef is what it merged
// into: checking both against local state is what stops a reused branch name,
// a post-merge push, or a PR against some other base from looking like merged
// work.
type PullRequest struct {
	Number  int
	State   PRState
	HeadOID string
	BaseRef string
}

// Forge looks up pull requests for a repository.
type Forge interface {
	// PRsForBranches returns, for each branch that has one, the most recent
	// pull request whose head is that branch. Branches without a pull request
	// are absent from the map.
	PRsForBranches(ctx context.Context, branches []string) (map[string]*PullRequest, error)
}

// Detect returns a Forge for the given remote URL, or nil when none is usable:
// the gh CLI is not on PATH, or the remote is not a GitHub repository. A nil
// return is the normal "no forge here" answer, not an error.
func Detect(_ context.Context, remoteURL string) Forge {
	owner, name, ok := strings.Cut(parseGitHubRepo(remoteURL), "/")
	if !ok {
		return nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return nil
	}
	return &GitHub{Owner: owner, Name: name}
}

// GitHub implements Forge by shelling out to the gh CLI.
type GitHub struct {
	Owner, Name string
}

// prBatchSize bounds the branches looked up per GraphQL request, keeping each
// query well inside GitHub's node and complexity limits.
const prBatchSize = 50

// PRsForBranches looks branches up in one GraphQL request per batch, asking
// for the newest pull request of any state with each head branch.
func (g *GitHub) PRsForBranches(ctx context.Context, branches []string) (map[string]*PullRequest, error) {
	prs := make(map[string]*PullRequest)
	for batch := range slices.Chunk(branches, prBatchSize) {
		// Branch names travel as variables, never spliced into the query text.
		args := []string{"api", "graphql", "-f", "owner=" + g.Owner, "-f", "name=" + g.Name, "-f", "query=" + prQuery(len(batch))}
		for i, branch := range batch {
			args = append(args, "-f", fmt.Sprintf("b%d=%s", i, branch))
		}

		cmd := exec.CommandContext(ctx, "gh", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("gh api graphql: %w\n%s", err, strings.TrimSpace(stderr.String()))
		}

		if err := parsePRBatch(stdout.Bytes(), batch, prs); err != nil {
			return nil, err
		}
	}
	return prs, nil
}

// prQuery builds a query with one aliased pullRequests field per branch,
// b0 through b<n-1>, each bound to the matching $b<i> variable.
func prQuery(n int) string {
	var b strings.Builder
	b.WriteString("query($owner:String!,$name:String!")
	for i := range n {
		fmt.Fprintf(&b, ",$b%d:String!", i)
	}
	b.WriteString("){repository(owner:$owner,name:$name){")
	for i := range n {
		fmt.Fprintf(&b, "b%d:pullRequests(headRefName:$b%d,first:1,orderBy:{field:CREATED_AT,direction:DESC}){nodes{number state headRefOid baseRefName}}", i, i)
	}
	b.WriteString("}}")
	return b.String()
}

// parsePRBatch decodes a prQuery response for branches into prs. A branch
// whose alias holds no nodes has no pull request and is left out.
func parsePRBatch(data []byte, branches []string, prs map[string]*PullRequest) error {
	var resp struct {
		Data struct {
			Repository map[string]struct {
				Nodes []struct {
					Number  int    `json:"number"`
					State   string `json:"state"`
					HeadOID string `json:"headRefOid"`
					BaseRef string `json:"baseRefName"`
				} `json:"nodes"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parsing gh output: %w", err)
	}
	if resp.Data.Repository == nil {
		return fmt.Errorf("parsing gh output: no repository in response")
	}
	for i, branch := range branches {
		nodes := resp.Data.Repository[fmt.Sprintf("b%d", i)].Nodes
		if len(nodes) == 0 {
			continue
		}
		prs[branch] = &PullRequest{
			Number:  nodes[0].Number,
			State:   PRState(strings.ToUpper(nodes[0].State)),
			HeadOID: nodes[0].HeadOID,
			BaseRef: nodes[0].BaseRef,
		}
	}
	return nil
}

var githubRemote = regexp.MustCompile(`^(?:(?:https?|ssh|git)://(?:[^@/]+@)?github\.com/|(?:[^@]+@)?github\.com:)([^/]+/[^/]+?)(?:\.git)?/?$`)

// parseGitHubRepo extracts "owner/name" from a GitHub remote URL in any of the
// forms git supports, and returns "" for any other host.
func parseGitHubRepo(remoteURL string) string {
	m := githubRemote.FindStringSubmatch(strings.TrimSpace(remoteURL))
	if m == nil {
		return ""
	}
	return m[1]
}
