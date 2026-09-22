package assistant

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/alplix/iris/internal/app"
)

// action is what the model must emit, verbatim as the ONLY content of its
// reply, when — and only when — the person's message clearly asked for a
// fleet change. Anything else in the reply means "this is not a
// machine-actionable request", and it is shown as ordinary chat text instead;
// nothing runs unless this exact, strict shape is matched.
type action struct {
	Tool    string            `json:"tool"`
	Host    string            `json:"host,omitempty"`
	Project string            `json:"project,omitempty"`
	Op      string            `json:"op,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	Fields  map[string]string `json:"fields,omitempty"`
}

const actionPrefix = "ACTION:"

// parseAction recognises a strict "ACTION: {json}" reply with nothing else
// around it. Any deviation — extra prose, a missing tool name, invalid JSON —
// is treated as plain text, never partially acted on.
func parseAction(reply string) (action, bool) {
	line := strings.TrimSpace(reply)
	if !strings.HasPrefix(line, actionPrefix) {
		return action{}, false
	}
	var a action
	if err := json.Unmarshal([]byte(strings.TrimSpace(line[len(actionPrefix):])), &a); err != nil {
		return action{}, false
	}
	return a, a.Tool != ""
}

var (
	projectOps  = map[string]bool{"suspend": true, "resume": true, "update": true, "detach": true, "nomorework": true, "allowmorework": true}
	runModes    = map[string]bool{"always": true, "auto": true, "never": true}
	clientOps   = map[string]bool{"setRunMode": true, "setNetworkMode": true, "benchmarks": true}
	prefKeyRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	allowedTool = map[string]bool{"project_op": true, "client_op": true, "client_op_all": true, "set_prefs": true}
)

// plan is a concrete, ready-to-run action: everything in it has already been
// checked against the fleet's actual current state.
type plan struct {
	Run func() error
}

// Clarify explains why an action could not be resolved, as data rather than
// a ready-made sentence, so the frontend can show it in the person's own
// language instead of a hard-coded English string.
type Clarify struct {
	Kind string `json:"kind"` // "unsupported_tool" | "invalid" | "host_not_found" | "project_not_found"
	Host string `json:"host,omitempty"`
	Name string `json:"name,omitempty"`
}

// resolve turns an action into a runnable plan, or explains why it can't run.
// A host or project the model names must match one Iris already knows about
// — this is what stops a model mistake (or a hypothetically compromised
// model) from ever running anything on a target that does not exist.
func resolve(mgr *app.Manager, a action) (*plan, *Clarify) {
	if !allowedTool[a.Tool] {
		return nil, &Clarify{Kind: "unsupported_tool", Name: a.Tool}
	}

	switch a.Tool {
	case "project_op":
		if !projectOps[a.Op] {
			return nil, &Clarify{Kind: "invalid"}
		}
		h, ok := findHost(mgr, a.Host)
		if !ok {
			return nil, &Clarify{Kind: "host_not_found", Name: a.Host}
		}
		p, ok := findProject(mgr.Snap(h.ID), a.Project)
		if !ok {
			return nil, &Clarify{Kind: "project_not_found", Host: h.Name, Name: a.Project}
		}
		return &plan{Run: func() error { return mgr.ProjectOp(h.ID, p.URL, a.Op) }}, nil

	case "client_op":
		if !clientOps[a.Op] || (a.Op != "benchmarks" && !runModes[a.Mode]) {
			return nil, &Clarify{Kind: "invalid"}
		}
		h, ok := findHost(mgr, a.Host)
		if !ok {
			return nil, &Clarify{Kind: "host_not_found", Name: a.Host}
		}
		return &plan{Run: func() error { return mgr.ClientOp(h.ID, a.Op, a.Mode) }}, nil

	case "client_op_all":
		if !clientOps[a.Op] || (a.Op != "benchmarks" && !runModes[a.Mode]) {
			return nil, &Clarify{Kind: "invalid"}
		}
		return &plan{Run: func() error {
			if failed := mgr.ClientOpAll(a.Op, a.Mode); len(failed) > 0 {
				names := make([]string, 0, len(failed))
				for n := range failed {
					names = append(names, n)
				}
				sort.Strings(names)
				return &partialFailure{hosts: names}
			}
			return nil
		}}, nil

	case "set_prefs":
		if len(a.Fields) == 0 {
			return nil, &Clarify{Kind: "invalid"}
		}
		h, ok := findHost(mgr, a.Host)
		if !ok {
			return nil, &Clarify{Kind: "host_not_found", Name: a.Host}
		}
		fields := make([][2]string, 0, len(a.Fields))
		for k, v := range a.Fields {
			if !prefKeyRe.MatchString(k) {
				return nil, &Clarify{Kind: "invalid"}
			}
			fields = append(fields, [2]string{k, v})
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i][0] < fields[j][0] })
		return &plan{Run: func() error { return mgr.PrefsSet(h.ID, fields) }}, nil
	}
	return nil, &Clarify{Kind: "unsupported_tool", Name: a.Tool}
}

type partialFailure struct{ hosts []string }

func (e *partialFailure) Error() string { return "failed on: " + strings.Join(e.hosts, ", ") }

func findHost(mgr *app.Manager, name string) (app.HostCfg, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return app.HostCfg{}, false
	}
	for _, h := range mgr.Store.List() {
		if strings.EqualFold(h.Name, name) {
			return h, true
		}
	}
	return app.HostCfg{}, false
}

func findProject(snap *app.Snapshot, name string) (app.ProjectInfo, bool) {
	name = strings.TrimSpace(name)
	if snap == nil || name == "" {
		return app.ProjectInfo{}, false
	}
	for _, p := range snap.Projects {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return app.ProjectInfo{}, false
}
