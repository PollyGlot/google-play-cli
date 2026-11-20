package output

import (
	"errors"
	"io"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/redact"
)

type ErrorEnvelope struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code        string       `json:"code"`
	ExitCode    int          `json:"exitCode"`
	Retryable   bool         `json:"retryable"`
	Operation   string       `json:"operation,omitempty"`
	Resource    *Resource    `json:"resource,omitempty"`
	Package     string       `json:"package,omitempty"`
	Message     string       `json:"message"`
	Reasons     []string     `json:"reasons,omitempty"`
	Requires    []string     `json:"requires,omitempty"`
	Replacement *Replacement `json:"replacement,omitempty"`
}

type Replacement struct {
	Kind      string `json:"kind"`
	Removed   string `json:"removed"`
	Use       string `json:"use"`
	RemovedIn string `json:"removedIn"`
	Note      string `json:"note,omitempty"`
}

type Resource struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func WriteErrorEnvelope(w io.Writer, err error) error {
	diag := exit.Classify(err)
	d := ErrorDetail{
		Code:      string(diag.Code),
		ExitCode:  diag.ExitCode,
		Retryable: diag.Retryable,
		Operation: redact.String(diag.Operation),
		Package:   diag.Package,
		Message:   redact.String(diag.Message),
		Reasons:   diag.Reasons,
	}
	if !diag.Resource.IsZero() {
		d.Resource = &Resource{Kind: string(diag.Resource.Kind), ID: diag.Resource.ID}
	}
	var safety *exit.SafetyFlagError
	if errors.As(err, &safety) && safety.Flag != "" {
		d.Requires = []string{safety.Flag}
	}
	var usage *exit.UsageError
	if errors.As(err, &usage) && usage.Replacement != nil {
		r := usage.Replacement
		d.Replacement = &Replacement{Kind: r.Kind, Removed: r.Removed, Use: r.Use, RemovedIn: r.RemovedIn, Note: r.Note}
	}
	return WriteJSON(w, ErrorEnvelope{Error: d})
}
