package convert

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/subscriptions/subscriptionscmd"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/subscriptions"
)

type Input struct {
	Package  string
	Price    string
	Currency string
}

type regionPrice struct {
	Region   string
	Amount   string
	Currency string
}

type Payload struct {
	Package string
	Rows    []regionPrice
	Raw     json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { _, err := w.Write(p.Raw); return err },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) renderTable(w io.Writer) error {
	for _, r := range p.Rows {
		if _, err := fmt.Fprintf(w, "%s\t%s %s\n", r.Region, r.Amount, r.Currency); err != nil {
			return err
		}
	}
	return nil
}

func (p Payload) renderMarkdown(w io.Writer) error {
	rows := make([][]string, 0, len(p.Rows))
	for _, r := range p.Rows {
		rows = append(rows, []string{r.Region, r.Amount, r.Currency})
	}
	if _, err := fmt.Fprintf(w, "## subscriptions prices convert: %s\n\n", p.Package); err != nil {
		return err
	}
	return output.MarkdownTable(w, []string{"REGION", "PRICE", "CURRENCY"}, rows)
}

func ParseMoney(amount, currency string) (subscriptions.Money, error) {
	amount = strings.TrimSpace(amount)
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return subscriptions.Money{}, exit.Usagef("invalid --currency %q: pass an ISO-4217 code like USD or EUR", currency)
	}
	units, frac, _ := strings.Cut(amount, ".")
	if units == "" || strings.Trim(units, "0123456789") != "" || strings.Trim(frac, "0123456789") != "" || len(frac) > 9 {
		return subscriptions.Money{}, exit.Usagef("invalid --price %q: pass a non-negative decimal amount like 4.99", amount)
	}
	var nanos int32
	if frac != "" {
		padded := frac + strings.Repeat("0", 9-len(frac))
		for _, d := range padded {
			nanos = nanos*10 + (d - '0')
		}
	}
	return subscriptions.Money{CurrencyCode: currency, Units: units, Nanos: nanos}, nil
}

type convertedView struct {
	ConvertedRegionPrices map[string]struct {
		RegionCode string `json:"regionCode"`
		Price      struct {
			CurrencyCode string `json:"currencyCode"`
			Units        string `json:"units"`
			Nanos        int32  `json:"nanos"`
		} `json:"price"`
	} `json:"convertedRegionPrices"`
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	money, err := ParseMoney(in.Price, in.Currency)
	if err != nil {
		return nil, err
	}
	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}
	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	raw, err := subscriptions.ConvertRegionPrices(rc.Ctx, httpClient, pkg, money)
	if err != nil {
		return nil, subscriptionscmd.Classify(pkg, err)
	}
	var view convertedView
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, api.DecodeFailure(api.Error{Operation: "monetization.convertRegionPrices", Package: pkg, Message: "decode response: " + err.Error(), Cause: err})
	}
	rows := make([]regionPrice, 0, len(view.ConvertedRegionPrices))
	for region, rp := range view.ConvertedRegionPrices {
		rows = append(rows, regionPrice{
			Region:   region,
			Amount:   fmt.Sprintf("%s.%02d", rp.Price.Units, rp.Price.Nanos/10000000),
			Currency: rp.Price.CurrencyCode,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Region < rows[j].Region })
	return Payload{Package: pkg, Rows: rows, Raw: raw}, nil
}

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "convert",
		Short: "Derive per-region prices from one base price",
		Long: `Convert one base price into per-region prices using today's exchange rates
and Google's country-specific pricing patterns (monetization.convertRegionPrices).
A computation, not a write: use it to fill a base plan's regionalConfigs in the
catalog files in bulk, then rehearse with "gplay subscriptions apply --dry-run".

--output json is the ConvertRegionPricesResponse verbatim: the regional Money
objects can be pasted into a catalog file's regionalConfigs.`,
		Example: `  gplay subscriptions prices convert --price 4.99 --currency USD
  gplay subscriptions prices convert --price 49.99 --currency EUR --output json`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.Price, "price", "", "base amount as a decimal, e.g. 4.99 (required)")
	cmd.Flags().StringVar(&in.Currency, "currency", "", "ISO-4217 currency of --price, e.g. USD (required)")
	_ = cmd.MarkFlagRequired("price")
	_ = cmd.MarkFlagRequired("currency")
	return cmd
}
