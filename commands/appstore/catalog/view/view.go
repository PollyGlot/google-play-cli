package view

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/appstore/appstorecmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appstorecatalog"
)

type Input struct {
	StorePackage string
	PlayPackage  string
}

type Payload struct {
	View appstorecatalog.CatalogAppView
	Raw  json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func headerRows(v appstorecatalog.CatalogAppView) [][2]string {
	return [][2]string{
		{"PACKAGE", v.PackageName},
		{"CATEGORY", category(v)},
		{"DEVELOPER", developerName(v)},
		{"ACTIVE_VERSIONS", strings.Join(v.ActiveVersionNames, ", ")},
		{"LAST_PUBLISH_TIME", v.LastPublishTime},
		{"FIRST_RELEASE_DATE", formatDate(v.FirstReleaseDate)},
		{"PRICE", priceOrFree(v.PriceInTheUnitedStates)},
		{"SALE_PRICE", formatMoney(v.SalePriceInTheUnitedStates)},
		{"IN_APP_PURCHASES", formatBool(v.HasInAppPurchases)},
		{"IN_APP_ADS", formatBool(v.HasInAppAds)},
		{"IARC_CERTIFICATE_ID", v.IARCCertificateID},
		{"ADULT_ONLY_AUDIENCE", formatBool(v.IsAdultOnlyAudience)},
		{"PRIVACY_POLICY_URL", v.PrivacyPolicyURL},
		{"DEFAULT_LANGUAGE", defaultLanguage(v)},
		{"DELIVERY_TOKEN", v.DeliveryToken},
	}
}

func (p Payload) renderTable(w io.Writer) error {
	for _, row := range headerRows(p.View) {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", row[0], row[1]); err != nil {
			return err
		}
	}
	for _, sec := range sections(p.View) {
		if _, err := fmt.Fprintf(w, "\n%s\n", sec.title); err != nil {
			return err
		}
		for _, line := range sec.lines {
			if _, err := fmt.Fprintf(w, "  %s\n", line); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p Payload) renderMarkdown(w io.Writer) error {
	heading := "Catalog app view"
	if p.View.PackageName != "" {
		heading += ": " + p.View.PackageName
	}
	if _, err := fmt.Fprintf(w, "## %s\n\n", heading); err != nil {
		return err
	}
	for _, row := range headerRows(p.View)[1:] {
		if _, err := fmt.Fprintf(w, "- **%s**: %s\n", mdLabel(row[0]), row[1]); err != nil {
			return err
		}
	}
	for _, sec := range sections(p.View) {
		if _, err := fmt.Fprintf(w, "\n**%s**\n\n", strings.TrimSuffix(sec.title, ":")); err != nil {
			return err
		}
		for _, line := range sec.lines {
			if _, err := fmt.Fprintf(w, "- %s\n", line); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderJSON(w io.Writer, p Payload) error {
	_, err := w.Write(p.Raw)
	return err
}

type section struct {
	title string
	lines []string
}

func sections(v appstorecatalog.CatalogAppView) []section {
	var out []section
	if lines := listingLines(v.LocalizedStoreListings); len(lines) > 0 {
		out = append(out, section{title: "Localized store listings:", lines: lines})
	}
	if lines := permissionLines(v.Permissions); len(lines) > 0 {
		out = append(out, section{title: "Permissions:", lines: lines})
	}
	if lines := permissionLines(v.PermissionsSdk23); len(lines) > 0 {
		out = append(out, section{title: "Permissions (SDK 23+):", lines: lines})
	}
	if lines := compatibilityLines(v.DeviceCompatibilityRequirements); len(lines) > 0 {
		out = append(out, section{title: "Device compatibility requirements:", lines: lines})
	}
	return out
}

func listingLines(l *appstorecatalog.LocalizedStoreListings) []string {
	if l == nil {
		return nil
	}
	lines := make([]string, 0, len(l.LocalizedListings))
	for _, sl := range l.LocalizedListings {
		label := sl.LanguageCode
		if sl.AppName != "" {
			label = strings.TrimSpace(label + "  " + oneLine(sl.AppName))
		}
		if sl.ShortDescription != "" {
			label = strings.TrimSpace(label + ": " + oneLine(sl.ShortDescription))
		}
		if label != "" {
			lines = append(lines, label)
		}
	}
	return lines
}

func permissionLines(perms []appstorecatalog.CatalogPermission) []string {
	lines := make([]string, 0, len(perms))
	for _, p := range perms {
		if p.Name == "" {
			continue
		}
		if p.MaxSdkVersion > 0 {
			lines = append(lines, p.Name+" (maxSdkVersion "+strconv.Itoa(p.MaxSdkVersion)+")")
			continue
		}
		lines = append(lines, p.Name)
	}
	return lines
}

func compatibilityLines(reqs []appstorecatalog.DeviceCompatibilityRequirements) []string {
	lines := make([]string, 0, len(reqs))
	for _, r := range reqs {
		var parts []string
		if s := formatSDK(r.SdkVersion); s != "" {
			parts = append(parts, s)
		}
		if len(r.NativePlatforms) > 0 {
			parts = append(parts, "abis: "+strings.Join(r.NativePlatforms, ", "))
		}
		if len(r.RequiredSystemFeatures) > 0 {
			parts = append(parts, "features: "+strings.Join(r.RequiredSystemFeatures, ", "))
		}
		if len(parts) == 0 {
			continue
		}
		lines = append(lines, strings.Join(parts, "; "))
	}
	return lines
}

func formatSDK(s *appstorecatalog.SdkVersion) string {
	if s == nil {
		return ""
	}
	lo, hi := s.MinSdkVersion, s.MaxSdkVersion
	var rng string
	switch {
	case lo != "" && hi != "":
		rng = "sdk " + lo + ".." + hi
	case lo != "":
		rng = "sdk " + lo + "+"
	case hi != "":
		rng = "sdk .." + hi
	}
	if s.TargetSdkVersion != "" {
		if rng == "" {
			return "target sdk " + s.TargetSdkVersion
		}
		return rng + " (target " + s.TargetSdkVersion + ")"
	}
	return rng
}

func category(v appstorecatalog.CatalogAppView) string {
	parts := make([]string, 0, 2)
	if v.AppCategory != "" {
		parts = append(parts, v.AppCategory)
	}
	if v.AppSubcategory != "" {
		parts = append(parts, v.AppSubcategory)
	}
	return strings.Join(parts, " / ")
}

func developerName(v appstorecatalog.CatalogAppView) string {
	if v.DeveloperDetails == nil {
		return ""
	}
	return v.DeveloperDetails.DeveloperName
}

func defaultLanguage(v appstorecatalog.CatalogAppView) string {
	if v.LocalizedStoreListings == nil {
		return ""
	}
	return v.LocalizedStoreListings.DefaultLanguageCode
}

func priceOrFree(m *appstorecatalog.Money) string {
	if m == nil {
		return "free"
	}
	return formatMoney(m)
}

func formatMoney(m *appstorecatalog.Money) string {
	if m == nil {
		return ""
	}
	units := strings.TrimSpace(m.Units)
	if units == "" {
		units = "0"
	}
	amount := units
	if m.Nanos != 0 {
		nanos := m.Nanos
		sign := ""
		if nanos < 0 {
			nanos = -nanos
			if !strings.HasPrefix(units, "-") {
				sign = "-"
			}
		}
		frac := strings.TrimRight(fmt.Sprintf("%09d", nanos), "0")
		amount = sign + units + "." + frac
	}
	if m.CurrencyCode != "" {
		return amount + " " + m.CurrencyCode
	}
	return amount
}

func formatDate(d *appstorecatalog.Date) string {
	if d == nil || d.Year == 0 {
		return ""
	}
	if d.Month == 0 {
		return fmt.Sprintf("%04d", d.Year)
	}
	if d.Day == 0 {
		return fmt.Sprintf("%04d-%02d", d.Year, d.Month)
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

func formatBool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func mdLabel(key string) string {
	words := strings.Split(strings.ToLower(key), "_")
	if len(words) > 0 && words[0] != "" {
		words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	}
	return strings.Join(words, " ")
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	playPkg, err := appstorecmd.RequirePlayPackage(in.PlayPackage,
		"no Play app package name: pass it as the positional argument (gplay appstore catalog view <play-package>)")
	if err != nil {
		return nil, err
	}
	storePkg, err := appstorecmd.ResolveStorePackage(in.StorePackage)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	v, raw, err := appstorecatalog.GetRecentAppView(rc.Ctx, httpClient, storePkg, playPkg)
	if err != nil {
		return nil, appstorecmd.ClassifyAppView(storePkg, playPkg, err)
	}
	var av appstorecatalog.CatalogAppView
	if v.AppView != nil {
		av = *v.AppView
	}
	return Payload{View: av, Raw: raw}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "view <play-package>",
		Short: "Read the catalog app view of one Play app (Catalog Export for app stores)",
		Long: `Read the catalog app view of one Play app from the Google Play Catalog
Export for app stores (appstorecatalog.recentappviews.get).

This surface serves the operator of an ALTERNATIVE APP STORE mirroring Play's
public catalog, not the app developer. Addressing therefore rides the app
store package name (the store on whose behalf the request is made) and NOT
the repo's .gplay/config.json pin: pass --store-package <pkg> or export
` + appstorecmd.EnvStorePackage + `. The Play app to look up is the positional
argument. Nothing is ever prompted for; a missing app store package name is a
usage error (exit 2), so a CI run fails fast.

The human views summarize the main catalog fields: identity and category,
developer, active version names, last publish time and first release date, the
US price (an app with no price is "free") and any active sale price, in-app
purchases/ads flags, the IARC certificate id and adult-only audience flag, the
privacy policy URL, the per-locale Listings, the declared permissions
(all-SDK and SDK 23+), the device compatibility requirement sets, and the
delivery token used with the Google Play Inline Install API.

--output json passes the RecentAppView through verbatim, including
everything the summary omits: image assets, device exclusions, screen support,
full descriptions. stdout carries the data, stderr the logs.

This is a direct read outside the Edit model: it opens no Edit. An app that is
not eligible for catalog inclusion fails with exit 30; a credential not
authorized for the app store's catalog export fails with exit 11.`,
		Example: `  gplay appstore catalog view com.example.game --store-package com.example.store
  gplay appstore catalog view com.example.game --store-package com.example.store --output json`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			in.PlayPackage = args[0]
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.StorePackage, "store-package", "",
		"package name of the app store on whose behalf the request is made (or $"+appstorecmd.EnvStorePackage+")")
	return cmd
}
