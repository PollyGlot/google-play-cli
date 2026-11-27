package vitalscmd

import (
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

const helpWidth = 78

var (
	periodZone     = regexp.MustCompile("\\*\\s+([A-Z][A-Z0-9_]*):[^*]*?timezone is `([^`]+)`")
	sentenceEnding = regexp.MustCompile(`\.(\s|$)`)
)

func metricDescription(desc, metric string) string {
	marker := "`" + metric + "` (`"
	i := strings.Index(desc, marker)
	if i < 0 {
		return ""
	}
	rest := desc[i+len(marker):]
	_, rest, ok := strings.Cut(rest, "`): ")
	if !ok {
		return ""
	}
	if loc := sentenceEnding.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]+1]
	}
	return strings.TrimSpace(rest)
}

func periodZones(desc string, periods []string) []string {
	zones := map[string]string{}
	for _, m := range periodZone.FindAllStringSubmatch(desc, -1) {
		zones[m[1]] = m[2]
	}
	var out []string
	for _, p := range periods {
		if z, ok := zones[p]; ok {
			out = append(out, p+" intervals use the "+z+" timezone")
		}
	}
	return out
}

func wrap(text string) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && len(line)+1+len(word) > helpWidth {
			lines = append(lines, line)
			line = word
			continue
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func PresetMetricHelp(idx schemaindex.Index, set vitals.MetricSet) string {
	metrics := vitals.SupportedMetrics(idx, set)
	if len(metrics) == 0 {
		return ""
	}
	primary := metrics[0]
	var metricsDesc, timelineDesc string
	if m, ok := idx.Methods[set.QueryMethodID()]; ok {
		s := idx.Schemas[m.Request]
		metricsDesc = s.Properties["metrics"].Description
		timelineDesc = s.Properties["timelineSpec"].Description
	}

	first := "It prints one metric, " + primary + ", as the raw value the Reporting API returns for each interval of the window, never rounded or averaged by gplay."
	if zones := periodZones(timelineDesc, vitals.SupportedPeriods(idx, set)); len(zones) > 0 {
		first += " " + strings.Join(zones, "; ") + "."
	}
	if d := metricDescription(metricsDesc, primary); d != "" {
		first += " The API defines " + primary + " as: " + d
	}
	required := vitals.RequiredDimensions(idx, set)
	for _, d := range required {
		first += " The API requires the " + d + " dimension for this set, so the preset prints one row per " + d + "."
	}

	others := "a rolling user-weighted average"
	for _, m := range metrics[1:] {
		if strings.HasPrefix(m, "userPerceived") && !strings.HasPrefix(primary, "userPerceived") {
			others += " or the user-perceived rate"
			break
		}
	}
	second := "The Play Console may show another metric of the set, such as " + others + ", so its figure can differ. The " + set.Name + " set has: " + strings.Join(metrics, ", ") + ". To print them side by side, use gplay vitals query:"

	example := "  gplay vitals query " + set.Name + " --package com.example.app --since 7d --metrics " + strings.Join(metrics, ",")
	if len(required) > 0 {
		example += " --dimensions " + strings.Join(required, ",")
	}
	return wrap(first) + "\n\n" + wrap(second) + "\n\n" + example
}

func setPresetHelp(cmd *cobra.Command, set vitals.MetricSet, intro, rest string) {
	static := intro + "\n\n" + rest
	cmd.Long = static
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if idx, err := schemaindex.Embedded(); err == nil {
			if metric := PresetMetricHelp(idx, set); metric != "" {
				c.Long = strings.Replace(c.Long, static, intro+"\n\n"+metric+"\n\n"+rest, 1)
			}
		}
		if c.HasParent() {
			c.Parent().HelpFunc()(c, args)
			return
		}
		c.SetHelpFunc(nil)
		c.HelpFunc()(c, args)
	})
}
