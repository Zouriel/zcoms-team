// Package github is a thin GitHub Projects v2 client that shells out to the
// authenticated `gh` CLI (gh handles the token). Every operation is best-effort:
// callers treat failures as non-fatal so local task assignment keeps working
// even when GitHub is unreachable.
package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Available reports whether the gh CLI is installed and authenticated.
func Available() bool {
	if _, err := exec.LookPath("gh"); err != nil {
		return false
	}
	return exec.Command("gh", "auth", "status").Run() == nil
}

// Field is a Projects v2 single-select field with its options (by lowercased name).
type Field struct {
	ID      string
	Options map[string]string // lowercased option name -> option id
}

// Project is a resolved Projects v2 board.
type Project struct {
	ID       string
	Status   *Field
	Priority *Field
}

// statusOptionFor maps a local task status to the GitHub Status option name.
func statusOptionName(localStatus string) string {
	switch localStatus {
	case "unassigned":
		return "todo"
	case "assigned":
		return "in progress"
	case "blocked":
		return "blocked"
	case "review":
		return "review"
	case "done":
		return "done"
	}
	return ""
}

// graphql runs a GraphQL query/mutation via gh, returning the raw `data` object.
func graphql(query string, strVars map[string]string, intVars map[string]int) (json.RawMessage, error) {
	args := []string{"api", "graphql", "-f", "query=" + query}
	for k, v := range strVars {
		args = append(args, "-f", k+"="+v)
	}
	for k, v := range intVars {
		args = append(args, "-F", k+"="+strconv.Itoa(v))
	}
	cmd := exec.Command("gh", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return nil, fmt.Errorf("gh graphql: %s", firstLine(msg))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		return nil, err
	}
	if len(env.Errors) > 0 {
		return nil, fmt.Errorf("graphql: %s", env.Errors[0].Message)
	}
	return env.Data, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

type projectNode struct {
	ID     string `json:"id"`
	Fields struct {
		Nodes []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Options []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"options"`
		} `json:"nodes"`
	} `json:"fields"`
}

const projectQuery = `query($owner:String!,$number:Int!){
  %s(login:$owner){ projectV2(number:$number){ id fields(first:50){ nodes{
    ... on ProjectV2SingleSelectField { id name options { id name } }
  } } } }
}`

// Resolve looks up a Projects v2 board by owner + number (trying organization
// then user) and extracts its Status / Priority single-select fields.
func Resolve(owner string, number int) (*Project, error) {
	for _, kind := range []string{"organization", "user"} {
		data, err := graphql(fmt.Sprintf(projectQuery, kind), map[string]string{"owner": owner}, map[string]int{"number": number})
		if err != nil {
			continue // not that kind of owner, or no access — try the next
		}
		var wrap map[string]struct {
			ProjectV2 *projectNode `json:"projectV2"`
		}
		if json.Unmarshal(data, &wrap) != nil {
			continue
		}
		pn := wrap[kind].ProjectV2
		if pn == nil || pn.ID == "" {
			continue
		}
		p := &Project{ID: pn.ID}
		for _, f := range pn.Fields.Nodes {
			field := &Field{ID: f.ID, Options: map[string]string{}}
			for _, o := range f.Options {
				field.Options[strings.ToLower(o.Name)] = o.ID
			}
			switch strings.ToLower(f.Name) {
			case "status":
				p.Status = field
			case "priority":
				p.Priority = field
			}
		}
		return p, nil
	}
	return nil, fmt.Errorf("project %s/#%d not found (or no access)", owner, number)
}

// CreateDraftItem adds a draft issue to the project and returns its item id.
func CreateDraftItem(projectID, title, body string) (string, error) {
	const m = `mutation($pid:ID!,$title:String!,$body:String!){
	  addProjectV2DraftIssue(input:{projectId:$pid,title:$title,body:$body}){ projectItem{ id } }
	}`
	data, err := graphql(m, map[string]string{"pid": projectID, "title": title, "body": body}, nil)
	if err != nil {
		return "", err
	}
	var r struct {
		Add struct {
			Item struct {
				ID string `json:"id"`
			} `json:"projectItem"`
		} `json:"addProjectV2DraftIssue"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", err
	}
	return r.Add.Item.ID, nil
}

// SetStatus sets the Status single-select to match a local task status.
func (p *Project) SetStatus(itemID, localStatus string) error {
	if p.Status == nil {
		return fmt.Errorf("project has no Status field")
	}
	opt, ok := p.Status.Options[statusOptionName(localStatus)]
	if !ok {
		return fmt.Errorf("no Status option for %q", localStatus)
	}
	return p.setSingleSelect(itemID, p.Status.ID, opt)
}

// SetPriority sets the Priority single-select if the project has one.
func (p *Project) SetPriority(itemID, priority string) error {
	if p.Priority == nil {
		return nil // optional field — silently skip
	}
	opt, ok := p.Priority.Options[strings.ToLower(priority)]
	if !ok {
		return nil
	}
	return p.setSingleSelect(itemID, p.Priority.ID, opt)
}

func (p *Project) setSingleSelect(itemID, fieldID, optionID string) error {
	const m = `mutation($pid:ID!,$item:ID!,$field:ID!,$opt:String!){
	  updateProjectV2ItemFieldValue(input:{projectId:$pid,itemId:$item,fieldId:$field,value:{singleSelectOptionId:$opt}}){ projectV2Item{ id } }
	}`
	_, err := graphql(m, map[string]string{"pid": p.ID, "item": itemID, "field": fieldID, "opt": optionID}, nil)
	return err
}
