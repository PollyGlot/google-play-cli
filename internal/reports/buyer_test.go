package reports

import (
	"strings"
	"testing"
)

func keysOf(cols []Column) string {
	ks := make([]string, len(cols))
	for i, c := range cols {
		ks[i] = c.Key
	}
	return strings.Join(ks, ",")
}

func mustDecode(t *testing.T, csv string) Table {
	t.Helper()
	tbl, err := Decode([]byte(csv))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return tbl
}

func TestMaskBuyerLocation_salesSpelling(t *testing.T) {
	tbl := mustDecode(t, "Order Number,City of Buyer,State of Buyer,Postal Code of Buyer,Country of Buyer,Charged Amount\n"+
		"GPA.1,Lyon,ARA,69001,FR,4.99\n")
	got, hidden := MaskBuyerLocation(tbl)
	if keysOf(hidden) != "cityOfBuyer,stateOfBuyer,postalCodeOfBuyer" {
		t.Errorf("hidden = %s", keysOf(hidden))
	}
	if keysOf(got.Columns) != "orderNumber,countryOfBuyer,chargedAmount" {
		t.Errorf("kept = %s", keysOf(got.Columns))
	}
	row := cellsOf(got.Rows[0], tbl.Columns)
	if len(row) != 3 || row["countryOfBuyer"] != "FR" || row["chargedAmount"] != "4.99" {
		t.Errorf("row = %v, want no location cell and the country kept", row)
	}
	if tbl.Rows[0].Value("cityOfBuyer") != "Lyon" {
		t.Error("MaskBuyerLocation must not mutate its input")
	}
}

func TestMaskBuyerLocation_earningsSpelling(t *testing.T) {
	tbl := mustDecode(t, "Description,Buyer Country,Buyer State,Buyer Postcode,Buyer Currency\nGPA.1,US,CA,94043,USD\n")
	got, hidden := MaskBuyerLocation(tbl)
	if keysOf(hidden) != "buyerState,buyerPostcode" || keysOf(got.Columns) != "description,buyerCountry,buyerCurrency" {
		t.Errorf("hidden = %s, kept = %s", keysOf(hidden), keysOf(got.Columns))
	}
}

func TestMaskBuyerLocation_documentedAndRenamedVariants(t *testing.T) {
	for _, h := range []string{
		// The spellings ADR-0050 §9 documents.
		"City of Buyer", "State of Buyer", "Postcode of Buyer", "Postal Code of Buyer",
		"Buyer State", "Buyer Postcode", "Buyer Postal Code",
		// Renames: other separators, camelCase, words glued together, and a
		// country named next to a place (the place still locates the buyer).
		"BUYER_CITY", "Buyer's Post Code", "State / Province of Buyer", "buyer zip",
		"BuyerCity", "CityOfBuyer", "buyercity", "cityofbuyer", "BuyerPostalCode", "buyer_postcode", "buyersstate",
		"Country / City of Buyer", "Buyer Country/State", "BuyerCountryCity",
	} {
		tbl := mustDecode(t, "Order Number,"+`"`+h+`"`+"\nGPA.1,x\n")
		if _, hidden := MaskBuyerLocation(tbl); len(hidden) != 1 || hidden[0].Header != h {
			t.Errorf("%q not masked (hidden %v)", h, hidden)
		}
	}
	for _, h := range []string{
		"Country of Buyer", "Buyer Country", "Buyer Currency", "Amount (Buyer Currency)", "City", "Seller State", "Statement Date",
		"BuyerCountry", "countryofbuyer", "Buyer Statement", "buyerstatement",
	} {
		tbl := mustDecode(t, "Order Number,"+`"`+h+`"`+"\nGPA.1,x\n")
		if _, hidden := MaskBuyerLocation(tbl); len(hidden) != 0 {
			t.Errorf("%q masked, want kept", h)
		}
	}
}
