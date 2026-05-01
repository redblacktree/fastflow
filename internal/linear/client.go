package linear

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const defaultEndpoint = "https://api.linear.app/graphql"

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client is a minimal Linear GraphQL client.
type Client struct {
	Endpoint string
	Token    string
	HTTP     httpDoer
}

// Issue represents a Linear issue.
type Issue struct {
	ID         string        `json:"id"`
	Identifier string        `json:"identifier"`
	Title      string        `json:"title"`
	State      WorkflowState `json:"state"`
	Parent     *Issue        `json:"parent,omitempty"`
	Children   []Issue       `json:"-"`
	Comments   []Comment     `json:"-"`
	URL        string        `json:"url,omitempty"`
}

// Comment represents a Linear comment.
type Comment struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	User      *User  `json:"user,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// User represents a Linear user.
type User struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

// WorkflowState represents a Linear workflow state.
type WorkflowState struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// CreateIssueInput holds the inputs for creating a new issue.
type CreateIssueInput struct {
	TeamID      string
	Title       string
	AssigneeID  string
	ParentID    string
	Description string
}

// New creates a Client with the Linear default endpoint and http.DefaultClient.
func New(token string) *Client {
	return &Client{
		Endpoint: defaultEndpoint,
		Token:    token,
		HTTP:     http.DefaultClient,
	}
}

// GraphQL query/mutation constants.
const getIssueWithChildrenQuery = `
query GetIssueWithChildren($id: String!) {
  issue(id: $id) {
    id identifier title url
    state { id name type }
    parent { id identifier }
    children(first: 50) { nodes { id identifier title state { id name type } } }
    comments(first: 100) { nodes { id body createdAt user { id name displayName } } }
  }
}`

const createIssueMutation = `
mutation CreateIssue($input: IssueCreateInput!) {
  issueCreate(input: $input) {
    success
    issue { id identifier title }
  }
}`

const updateIssueStateMutation = `
mutation UpdateIssueState($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: { stateId: $stateId }) {
    success
  }
}`

const createCommentMutation = `
mutation CreateComment($id: String!, $body: String!) {
  commentCreate(input: { issueId: $id, body: $body }) {
    success
    comment { id body createdAt }
  }
}`

// doGraphQL posts a GraphQL request and unmarshals the response data into out.
func (c *Client) doGraphQL(query string, vars map[string]interface{}, out interface{}) error {
	payload, err := json.Marshal(map[string]interface{}{
		"query":     query,
		"variables": vars,
	})
	if err != nil {
		return fmt.Errorf("linear: marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("linear: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("linear: http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("linear: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("linear: http %d: %s", resp.StatusCode, string(body))
	}

	var errCheck struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if jsonErr := json.Unmarshal(body, &errCheck); jsonErr == nil && len(errCheck.Errors) > 0 {
		return fmt.Errorf("linear: graphql error: %s", errCheck.Errors[0].Message)
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("linear: unmarshal response: %w", err)
	}
	return nil
}

// Internal types that mirror the GraphQL response shape.

type gqlIssue struct {
	ID         string        `json:"id"`
	Identifier string        `json:"identifier"`
	Title      string        `json:"title"`
	URL        string        `json:"url"`
	State      WorkflowState `json:"state"`
	Parent     *struct {
		ID         string `json:"id"`
		Identifier string `json:"identifier"`
	} `json:"parent"`
	Children struct {
		Nodes []gqlIssue `json:"nodes"`
	} `json:"children"`
	Comments struct {
		Nodes []Comment `json:"nodes"`
	} `json:"comments"`
}

func (g gqlIssue) toIssue() *Issue {
	iss := &Issue{
		ID:         g.ID,
		Identifier: g.Identifier,
		Title:      g.Title,
		URL:        g.URL,
		State:      g.State,
		Comments:   g.Comments.Nodes,
	}
	if g.Parent != nil {
		iss.Parent = &Issue{
			ID:         g.Parent.ID,
			Identifier: g.Parent.Identifier,
		}
	}
	for _, child := range g.Children.Nodes {
		iss.Children = append(iss.Children, *child.toIssue())
	}
	return iss
}

// GetIssueWithChildren fetches an issue with its children and comments.
// identifier may be either a Linear UUID or a human identifier like "REL-548".
func (c *Client) GetIssueWithChildren(identifier string) (*Issue, error) {
	var resp struct {
		Data struct {
			Issue gqlIssue `json:"issue"`
		} `json:"data"`
	}
	if err := c.doGraphQL(getIssueWithChildrenQuery, map[string]interface{}{"id": identifier}, &resp); err != nil {
		return nil, err
	}
	if resp.Data.Issue.ID == "" {
		return nil, fmt.Errorf("linear: issue %q not found", identifier)
	}
	return resp.Data.Issue.toIssue(), nil
}

// CreateIssue creates a new Linear issue.
func (c *Client) CreateIssue(in CreateIssueInput) (*Issue, error) {
	input := map[string]interface{}{
		"teamId": in.TeamID,
		"title":  in.Title,
	}
	if in.AssigneeID != "" {
		input["assigneeId"] = in.AssigneeID
	}
	if in.ParentID != "" {
		input["parentId"] = in.ParentID
	}
	if in.Description != "" {
		input["description"] = in.Description
	}

	var resp struct {
		Data struct {
			IssueCreate struct {
				Success bool `json:"success"`
				Issue   struct {
					ID         string `json:"id"`
					Identifier string `json:"identifier"`
					Title      string `json:"title"`
				} `json:"issue"`
			} `json:"issueCreate"`
		} `json:"data"`
	}
	if err := c.doGraphQL(createIssueMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return nil, err
	}
	if !resp.Data.IssueCreate.Success {
		return nil, fmt.Errorf("linear: issueCreate returned success=false")
	}
	iss := resp.Data.IssueCreate.Issue
	return &Issue{ID: iss.ID, Identifier: iss.Identifier, Title: iss.Title}, nil
}

// UpdateIssueState moves an issue to a new workflow state.
func (c *Client) UpdateIssueState(issueID, stateID string) error {
	var resp struct {
		Data struct {
			IssueUpdate struct {
				Success bool `json:"success"`
			} `json:"issueUpdate"`
		} `json:"data"`
	}
	if err := c.doGraphQL(updateIssueStateMutation, map[string]interface{}{
		"id":      issueID,
		"stateId": stateID,
	}, &resp); err != nil {
		return err
	}
	if !resp.Data.IssueUpdate.Success {
		return fmt.Errorf("linear: issueUpdate returned success=false")
	}
	return nil
}

// CreateComment posts a comment on an issue.
func (c *Client) CreateComment(issueID, body string) (*Comment, error) {
	var resp struct {
		Data struct {
			CommentCreate struct {
				Success bool    `json:"success"`
				Comment Comment `json:"comment"`
			} `json:"commentCreate"`
		} `json:"data"`
	}
	if err := c.doGraphQL(createCommentMutation, map[string]interface{}{
		"id":   issueID,
		"body": body,
	}, &resp); err != nil {
		return nil, err
	}
	if !resp.Data.CommentCreate.Success {
		return nil, fmt.Errorf("linear: commentCreate returned success=false")
	}
	result := resp.Data.CommentCreate.Comment
	return &result, nil
}
