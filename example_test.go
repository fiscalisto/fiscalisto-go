package fiscalisto_test

import (
	"context"
	"fmt"

	fiscalisto "fiscalisto.com/go"
)

func Example() {
	client, err := fiscalisto.NewClient("fk_test_…")
	if err != nil {
		return
	}
	invoice, err := client.Invoices.Create(context.Background(), &fiscalisto.InvoiceCreate{
		Customer:    fiscalisto.IDOf[fiscalisto.InvoiceCreateCustomer]("cus_…"),
		PaymentForm: fiscalisto.String("03"),
		Items: []fiscalisto.InvoiceCreateItem{{
			Product:  fiscalisto.String("prd_…"),
			Quantity: fiscalisto.Float64(1),
		}},
	})
	if err == nil {
		fmt.Println(invoice.ID)
	}
}
