# Fiscalisto para Go

El SDK de la [API de facturación de Fiscalisto](https://fiscalisto.com/docs/api): timbre CFDI 4.0 (facturas, pagos, notas de crédito, nómina, retenciones y lotes) desde Go.

- Un struct para lo que manda y para lo que recibe, con la descripción de cada campo y su nombre en el XML del SAT. Lo opcional es un apuntador: `fiscalisto.String("03")`.
- `Idempotency-Key` automática en lo que timbra: un reintento nunca timbra dos veces.
- Reintentos solo de lo que es seguro repetir; respeta `Retry-After`.
- Listas que se recorren solas con `for … range`.
- Errores con `errors.As` y códigos como constantes (`fiscalisto.CodeInvoiceRejected`).
- Webhooks verificados en una línea.
- Sin dependencias: Go 1.23 o más reciente.

```bash
go get fiscalisto.com/go
```

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	fiscalisto "fiscalisto.com/go"
)

func main() {
	client, err := fiscalisto.NewClient("") // la llave viene de FISCALISTO_API_KEY
	if err != nil {
		log.Fatal(err)
	}
	invoice, err := client.Invoices.Create(context.Background(), &fiscalisto.InvoiceCreate{
		Customer: fiscalisto.ObjectOf(fiscalisto.InvoiceCreateCustomer{
			TaxID: "ICV060329BY0", LegalName: "INMOBILIARIA CVA", TaxSystem: "601", Zip: "33826",
		}),
		PaymentForm: fiscalisto.String("03"),
		Items: []fiscalisto.InvoiceCreateItem{{
			Description: fiscalisto.String("Mensualidad"),
			ProductKey:  fiscalisto.String("86121700"),
			Quantity:    fiscalisto.Float64(2),
			UnitPrice:   fiscalisto.Float64(2500),
		}},
	}, fiscalisto.WithIdempotencyKey("pedido-A-1042"))
	var apiErr *fiscalisto.Error
	if errors.As(err, &apiErr) && apiErr.Code == fiscalisto.CodeInvoiceRejected {
		log.Fatalf("El SAT no la aceptó (%s): %s", apiErr.SatCode, apiErr.Message)
	} else if err != nil {
		log.Fatal(err)
	}
	fmt.Println(*invoice.UUID, *invoice.Total)

	for inv, err := range client.Invoices.ListAutoPaging(context.Background(), &fiscalisto.ListInvoicesParams{Status: fiscalisto.String("valid")}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(inv.ID)
	}
}
```

Un webhook:

```go
payload, _ := io.ReadAll(r.Body)
event, err := fiscalisto.VerifyWebhook(payload, r.Header, os.Getenv("FISCALISTO_WEBHOOK_SECRET"))
if err != nil {
	http.Error(w, "firma inválida", http.StatusBadRequest)
	return
}
if event.Type == "invoice.stamped" {
	invoice, _ := fiscalisto.DataObject[fiscalisto.Invoice](event)
	_ = invoice
}
```

La guía completa está en [fiscalisto.com/docs/api/sdk](https://fiscalisto.com/docs/api/sdk).

## Desarrollo

`*.gen.go` se genera desde la especificación OpenAPI con `bun run openapi` en la raíz del repositorio; no lo edite. Lo demás se escribe a mano.

```bash
go vet ./... && go test ./...
```
