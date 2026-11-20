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

type Service struct {
	Name    string
	Host    string
	Version string
}

var Services = []Service{
	{Name: "androidpublisher", Host: "androidpublisher.googleapis.com", Version: "v3"},
	{Name: "playdeveloperreporting", Host: "playdeveloperreporting.googleapis.com", Version: "v1beta1"},
	{Name: "gamesConfiguration", Host: "gamesconfiguration.googleapis.com", Version: "v1configuration"},
	{Name: "playcustomapp", Host: "playcustomapp.googleapis.com", Version: "v1"},
	{Name: "gamesManagement", Host: "gamesmanagement.googleapis.com", Version: "v1management"},
}

func (s Service) SnapshotFilename() string {
	return fmt.Sprintf("%s_%s.json", s.Name, s.Version)
}

func (s Service) DiscoveryURL() string {
	return fmt.Sprintf("https://%s/$discovery/rest?version=%s", s.Host, s.Version)
}

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

func Normalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode discovery doc: %w", err)
	}
	delete(doc, "etag")

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode discovery doc: %w", err)
	}
	return buf.Bytes(), nil
}

type method struct {
	ID         string `json:"id"`
	HTTPMethod string `json:"httpMethod"`
	Path       string `json:"path"`
}

type resource struct {
	Methods   map[string]method   `json:"methods"`
	Resources map[string]resource `json:"resources"`
}

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
