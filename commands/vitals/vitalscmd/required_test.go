package vitalscmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

func sentDimensions(t *testing.T, call testkit.Call) []string {
	t.Helper()
	var body struct {
		Dimensions []string `json:"dimensions"`
	}
	if err := json.Unmarshal(call.Body, &body); err != nil {
		t.Fatalf("decode %s body: %v", call.Path, err)
	}
	return body.Dimensions
}

const slowStartRowsBody = `{"rows":[
  {"startTime":{"year":2026,"month":6,"day":1},"dimensions":[{"dimension":"startType","stringValue":"COLD"}],"metrics":[{"metric":"slowStartRate","decimalValue":{"value":"0.04"}}]},
  {"startTime":{"year":2026,"month":6,"day":1},"dimensions":[{"dimension":"startType","stringValue":"WARM"}],"metrics":[{"metric":"slowStartRate","decimalValue":{"value":"0.02"}}]}
]}`

func TestRunPreset_slowstart_sendsStartTypeAndPrintsItsColumn(t *testing.T) {
	fake := testkit.NewFake(testkit.Any(http.StatusOK, slowStartRowsBody))
	rc := newRC(t, fake)
	r, err := runPreset(rc, set(t, "slowstartrate"), presetInput{Package: "com.example.app"})
	if err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	if got := sentDimensions(t, lastCall(t, fake)); strings.Join(got, ",") != "startType" {
		t.Errorf("dimensions = %v, want [startType]", got)
	}
	var buf bytes.Buffer
	if err := r.Renderers().Table(&buf); err != nil {
		t.Fatalf("table: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "STARTTYPE") || !strings.Contains(out, "COLD") || !strings.Contains(out, "WARM") {
		t.Errorf("table must print one row per startType:\n%s", out)
	}
}

func TestRunPreset_slowstartBy_keepsTheSliceAndAddsStartType(t *testing.T) {
	fake := newPresetFake()
	rc := newRC(t, fake)
	if _, err := runPreset(rc, set(t, "slowstartrate"), presetInput{Package: "com.example.app", By: "versionCode"}); err != nil {
		t.Fatalf("runPreset: %v", err)
	}
	if got := sentDimensions(t, lastCall(t, fake)); strings.Join(got, ",") != "versionCode,startType" {
		t.Errorf("dimensions = %v, want [versionCode startType]", got)
	}
}

var requiredSection = regexp.MustCompile(`\*\*Required dimension:\*\*([^*]|\*[^*])*`)
var requiredEntry = regexp.MustCompile("\\*\\s+`([A-Za-z0-9]+)`")

func TestExecute_everyRequiredDimensionIsSent(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	declaring := 0
	for _, ms := range append(vitals.MetricSets(), vitals.ErrorCountSet()) {
		desc := idx.Schemas[idx.Methods[ms.GetMethodID()].Response].Description
		section := requiredSection.FindString(desc)
		if section == "" {
			continue
		}
		declaring++
		t.Run(ms.Name, func(t *testing.T) {
			fake := newPresetFake()
			rc := newRC(t, fake)
			if _, err := Execute(rc, Params{Set: ms, Package: "com.example.app"}); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			sent := strings.Join(sentDimensions(t, lastCall(t, fake)), ",")
			entries := requiredEntry.FindAllStringSubmatch(section, -1)
			if len(entries) == 0 {
				t.Fatalf("%s declares a required dimension gplay cannot read: %s", ms.Name, section)
			}
			for _, m := range entries {
				if !contains(strings.Split(sent, ","), m[1]) {
					t.Errorf("%s requires dimension %q, gplay sent %q", ms.Name, m[1], sent)
				}
			}
		})
	}
	if declaring == 0 {
		t.Fatal("no metric set declares a required dimension: the snapshot marker moved, so this guard checks nothing")
	}
}
