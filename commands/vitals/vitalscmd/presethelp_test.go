package vitalscmd_test

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/vitals/query"
	"github.com/PollyGlot/google-play-cli/commands/vitals/vitalscmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

func renderPresetHelp(t *testing.T, spec vitalscmd.PresetSpec) string {
	t.Helper()
	parent := &cobra.Command{Use: "vitals"}
	cmd := vitalscmd.NewPresetCommand(kernel.Boot{}, spec)
	parent.AddCommand(cmd)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Help(); err != nil {
		t.Fatalf("%s --help: %v", spec.Use, err)
	}
	return buf.String()
}

func queryExample(t *testing.T, help string) []string {
	t.Helper()
	for _, line := range strings.Split(help, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "gplay vitals query ") {
			return strings.Fields(line)[3:]
		}
	}
	t.Fatalf("help has no gplay vitals query example:\n%s", help)
	return nil
}

func TestPresetHelp_namesItsMetricAndHowToGetTheOthers(t *testing.T) {
	idx, err := schemaindex.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range vitalscmd.Presets {
		t.Run(spec.Use, func(t *testing.T) {
			s, ok := vitals.MetricSetByName(spec.Set)
			if !ok {
				t.Fatalf("metric set %q not in registry", spec.Set)
			}
			metrics := vitals.SupportedMetrics(idx, s)
			if len(metrics) < 2 {
				t.Fatalf("%s catalog lists %v, want a primary metric and alternatives", spec.Set, metrics)
			}
			help := renderPresetHelp(t, spec)
			flat := strings.Join(strings.Fields(help), " ")
			for _, want := range []string{
				"It prints one metric, " + metrics[0] + ",",
				"raw value the Reporting API returns",
				"DAILY intervals use the America/Los_Angeles timezone",
				"The API defines " + metrics[0] + " as: Percentage of distinct users",
			} {
				if !strings.Contains(flat, want) {
					t.Errorf("%s --help lacks %q:\n%s", spec.Use, want, help)
				}
			}

			args := queryExample(t, help)
			q := query.NewCommand(kernel.Boot{})
			if err := q.ParseFlags(args[1:]); err != nil {
				t.Fatalf("query example %v: %v", args, err)
			}
			if err := q.ValidateArgs(append([]string{args[0]}, q.Flags().Args()...)); err != nil {
				t.Fatalf("query example %v: %v", args, err)
			}
			if args[0] != spec.Set {
				t.Errorf("query example targets %q, want %q", args[0], spec.Set)
			}
			got, err := q.Flags().GetStringSlice("metrics")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, metrics) {
				t.Errorf("query example --metrics = %v, want the %s catalog %v", got, spec.Set, metrics)
			}
		})
	}
}

func TestPresetHelp_crashesPointsAtTheConsoleMetrics(t *testing.T) {
	if vitalscmd.Presets[0].Use != "crashes" {
		t.Fatalf("Presets[0] = %q, want crashes", vitalscmd.Presets[0].Use)
	}
	help := strings.Join(strings.Fields(renderPresetHelp(t, vitalscmd.Presets[0])), " ")
	for _, want := range []string{"crashRate28dUserWeighted", "userPerceivedCrashRate", "user-perceived rate", "HOURLY intervals use the UTC timezone"} {
		if !strings.Contains(help, want) {
			t.Errorf("vitals crashes --help lacks %q:\n%s", want, help)
		}
	}
}
