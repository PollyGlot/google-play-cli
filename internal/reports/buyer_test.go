package reports

import (
	"errors"
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
	row := got.Rows[0]
	if len(row) != 3 || row["countryOfBuyer"] != "FR" || row["chargedAmount"] != "4.99" {
		t.Errorf("row = %v, want no location cell and the country kept", row)
	}
	if tbl.Rows[0]["cityOfBuyer"] != "Lyon" {
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
		"City of Buyer", "Buyer State", "Buyer Postcode", "Postcode of Buyer",
		"Buyer Postal Code", "BUYER_CITY", "Buyer's Post Code", "State / Province of Buyer", "buyer zip",
	} {
		tbl := mustDecode(t, "Order Number,"+`"`+h+`"`+"\nGPA.1,x\n")
		if _, hidden := MaskBuyerLocation(tbl); len(hidden) != 1 || hidden[0].Header != h {
			t.Errorf("%q not masked (hidden %v)", h, hidden)
		}
	}
	for _, h := range []string{
		"Country of Buyer", "Buyer Country", "Buyer Currency", "City", "Seller State", "Statement Date", "Country / City of Buyer",
	} {
		tbl := mustDecode(t, "Order Number,"+`"`+h+`"`+"\nGPA.1,x\n")
		if _, hidden := MaskBuyerLocation(tbl); len(hidden) != 0 {
			t.Errorf("%q masked, want kept", h)
		}
	}
}

func TestFilterPackage_onTheHeaderDerivedColumn(t *testing.T) {
	for name, csv := range map[string]string{
		"Product ID (finance spelling)": "Order Number,Product ID,Charged Amount\nGPA.1,com.example.app,1\nGPA.2,com.example.pro,2\nGPA.3,com.example.app,3\n",
		"Package Name":                  "Order Number,Charged Amount,Package Name\nGPA.1,1,com.example.app\nGPA.2,2,com.example.pro\nGPA.3,3,com.example.app\n",
		"Package ID beats Product ID":   "Order Number,Product ID,Package ID\nGPA.1,x,com.example.app\nGPA.2,com.example.app,com.example.pro\nGPA.3,x,com.example.app\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := FilterPackage(mustDecode(t, csv), "com.example.app")
			if err != nil {
				t.Fatalf("FilterPackage: %v", err)
			}
			if len(got.Rows) != 2 || got.Rows[0]["orderNumber"] != "GPA.1" || got.Rows[1]["orderNumber"] != "GPA.3" {
				t.Errorf("rows = %v", got.Rows)
			}
		})
	}
}

func TestFilterPackage_noPackageColumn(t *testing.T) {
	_, err := FilterPackage(mustDecode(t, "Order Number,Charged Amount\nGPA.1,1\n"), "com.example.app")
	var npe *NoPackageColumnError
	if !errors.As(err, &npe) || npe.ExitCode() != 2 {
		t.Fatalf("err = %v, want a NoPackageColumnError (exit 2)", err)
	}
	if !strings.Contains(err.Error(), "--package") || !strings.Contains(err.Error(), "Order Number") {
		t.Errorf("message should name --package and the headers present: %q", err)
	}
}
