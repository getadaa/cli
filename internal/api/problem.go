package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Problem is an RFC 9457 error from the API. Its text is meant for people;
// code branches on Type.
type Problem struct {
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	Status        int            `json:"status"`
	Detail        string         `json:"detail,omitempty"`
	HowToResolve  string         `json:"how_to_resolve,omitempty"`
	ResolvableBy  string         `json:"resolvable_by,omitempty"`
	PossibleFrom  *string        `json:"possible_from,omitempty"`
	CurrentStatus string         `json:"current_status,omitempty"`
	WantedStatus  string         `json:"wanted_status,omitempty"`
	Instance      string         `json:"instance,omitempty"`
	Errors        []FieldProblem `json:"errors,omitempty"`
	// Raw is the body as received, for --json error output.
	Raw json.RawMessage `json:"-"`
}

// FieldProblem is one invalid input. The API names it by location, such as
// "body.email"; the other fields are accepted for older servers.
type FieldProblem struct {
	Location string `json:"location,omitempty"`
	Message  string `json:"message,omitempty"`
	Value    any    `json:"value,omitempty"`
	Field    string `json:"field,omitempty"`
	Pointer  string `json:"pointer,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Name is the field in terms a person typed: "email", not "body.email".
func (f FieldProblem) Name() string {
	switch {
	case f.Location != "":
		return strings.TrimPrefix(f.Location, "body.")
	case f.Field != "":
		return f.Field
	}
	return strings.TrimPrefix(f.Pointer, "/")
}

func (p *Problem) Error() string {
	msg := p.Title
	if p.Detail != "" && p.Detail != p.Title {
		msg += ": " + p.Detail
	}
	return msg
}

// Code is the short, stable part of Type, such as "not-found".
func (p *Problem) Code() string {
	return p.Type[strings.LastIndex(p.Type, "/")+1:]
}

func parseProblem(status int, body []byte) *Problem {
	p := &Problem{}
	if json.Unmarshal(body, p) != nil || p.Title == "" {
		text := strings.TrimSpace(string(body))
		if len(text) > 300 {
			text = text[:300] + "…"
		}
		p = &Problem{
			Type:   "about:blank",
			Title:  http.StatusText(status),
			Detail: text,
		}
		if status >= 500 {
			p.ResolvableBy = "adaa"
		}
	}
	if p.Status == 0 {
		p.Status = status
	}
	p.Raw = body
	return p
}
