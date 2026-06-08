// Package discovery fetches, normalizes, and derives an offline snapshot of a
// Google API Discovery document (see issue #52).
//
// Nothing in this package is imported by cmd/gplay: it is build-and-maintenance
// tooling, never compiled into the shipped binary (ADR-0007 spirit). The
// fetch/normalize/derive logic lives here — importable — so the offline
// integrity test can re-derive committed artifacts without a network call; the
// `discovery-update` command is a thin main wrapper over these functions.
package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Service identifies one Discovery document to snapshot. The tooling is
// multi-doc by design: Services declares the list, and adding a service later
// (e.g. androidvitals for #49) is a one-line append.
type Service struct {
	Name    string // canonical service name, e.g. "androidpublisher"
	Host    string // API host, e.g. "androidpublisher.googleapis.com"
	Version string // API version, e.g. "v3"
}

// Services is the declared snapshot set. v1 = androidpublisher v3 only.
var Services = []Service{
	{Name: "androidpublisher", Host: "androidpublisher.googleapis.com", Version: "v3"},
}

// SnapshotFilename is the per-service snapshot file name, e.g.
// "androidpublisher_v3.json".
func (s Service) SnapshotFilename() string {
	return fmt.Sprintf("%s_%s.json", s.Name, s.Version)
}

// DiscoveryURL is the Google Discovery REST endpoint for the service.
func (s Service) DiscoveryURL() string {
	return fmt.Sprintf("https://%s/$discovery/rest?version=%s", s.Host, s.Version)
}

// Fetch GETs the raw Discovery document for s. The returned bytes are the
// upstream body verbatim — call Normalize before persisting.
func Fetch(ctx context.Context, c *http.Client, s Service) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.DiscoveryURL(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", s.DiscoveryURL(), err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.DiscoveryURL(), err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d: %s", s.DiscoveryURL(), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// Normalize produces the deterministic, reviewable snapshot form of a raw
// Discovery document:
//
//   - object keys are sorted (encoding/json marshals map keys in sorted order),
//     so each regen yields a minimal git diff;
//   - the transport-level top-level "etag" is dropped (noise);
//   - numbers are preserved verbatim (json.Number), so no float reformatting;
//   - HTML escaping is disabled, keeping descriptions readable.
//
// Normalize is idempotent: Normalize(Normalize(x)) == Normalize(x), which is
// exactly what the offline integrity gate asserts against the committed file.
func Normalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode discovery doc: %w", err)
	}
	delete(doc, "etag") // transport-level, not part of the API schema

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode discovery doc: %w", err)
	}
	return buf.Bytes(), nil
}

// method mirrors the fields of a Discovery method we project into paths.txt.
type method struct {
	ID         string `json:"id"`
	HTTPMethod string `json:"httpMethod"`
	Path       string `json:"path"`
}

// resource mirrors the recursive Discovery resource tree.
type resource struct {
	Methods   map[string]method   `json:"methods"`
	Resources map[string]resource `json:"resources"`
}

// methodLines returns one "id\thttpMethod\tpath" line per method in a single
// normalized snapshot, sorted by id.
func methodLines(normalized []byte) ([]string, error) {
	var doc struct {
		Resources map[string]resource `json:"resources"`
	}
	if err := json.Unmarshal(normalized, &doc); err != nil {
		return nil, fmt.Errorf("parse snapshot for paths: %w", err)
	}
	var lines []string
	var walk func(map[string]resource)
	walk = func(rs map[string]resource) {
		for _, r := range rs {
			for _, m := range r.Methods {
				lines = append(lines, fmt.Sprintf("%s\t%s\t%s", m.ID, m.HTTPMethod, m.Path))
			}
			walk(r.Resources)
		}
	}
	walk(doc.Resources)
	sort.Strings(lines)
	return lines, nil
}

// RenderPaths derives the existence-check index (paths.txt) from one or more
// normalized snapshots. Method ids carry their service prefix, so aggregating
// across services and sorting globally stays unambiguous. The output ends with
// a trailing newline. Both the regen tool and the integrity test call this, so
// a re-derivation is byte-equal to the committed file.
func RenderPaths(normalizedSnapshots [][]byte) ([]byte, error) {
	var all []string
	for _, snap := range normalizedSnapshots {
		lines, err := methodLines(snap)
		if err != nil {
			return nil, err
		}
		all = append(all, lines...)
	}
	sort.Strings(all)
	return []byte(strings.Join(all, "\n") + "\n"), nil
}
