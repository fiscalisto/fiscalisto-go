package fiscalisto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrWebhookSignature: el webhook no es de Fiscalisto (falta un encabezado, la hora o la firma
// no cuadran). Compárelo con errors.Is.
var ErrWebhookSignature = errors.New("fiscalisto: el webhook no es de Fiscalisto")

type webhookError struct{ msg string }

func (e *webhookError) Error() string        { return "fiscalisto: " + e.msg }
func (e *webhookError) Is(target error) bool { return target == ErrWebhookSignature }

// DefaultWebhookTolerance: cuánto puede diferir la hora del webhook de la suya.
const DefaultWebhookTolerance = 5 * time.Minute

// VerifyWebhook revisa la firma y la hora de un webhook (Standard Webhooks) y le devuelve el
// evento. Pase el cuerpo exactamente como llegó, no el JSON ya interpretado: un espacio de
// diferencia cambia la firma.
//
//	payload, _ := io.ReadAll(r.Body)
//	event, err := fiscalisto.VerifyWebhook(payload, r.Header, os.Getenv("FISCALISTO_WEBHOOK_SECRET"))
func VerifyWebhook(payload []byte, header http.Header, secret string) (*Event, error) {
	return VerifyWebhookAt(payload, header, secret, time.Now(), DefaultWebhookTolerance)
}

// VerifyWebhookAt es VerifyWebhook con la hora y la tolerancia que usted diga (pruebas).
func VerifyWebhookAt(payload []byte, header http.Header, secret string, now time.Time, tolerance time.Duration) (*Event, error) {
	id := headerValue(header, "Webhook-Id")
	timestamp := headerValue(header, "Webhook-Timestamp")
	signatures := headerValue(header, "Webhook-Signature")
	if id == "" || timestamp == "" || signatures == "" {
		return nil, &webhookError{"faltan los encabezados webhook-id, webhook-timestamp o webhook-signature"}
	}
	sent, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || now.Sub(time.Unix(sent, 0)).Abs() > tolerance {
		return nil, &webhookError{"la hora del webhook está fuera de tolerancia: puede ser una repetición"}
	}
	encoded, ok := strings.CutPrefix(secret, "whsec_")
	if !ok {
		return nil, &webhookError{"el secreto del endpoint empieza con whsec_"}
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, &webhookError{"el secreto del endpoint no es válido"}
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(payload)
	expected := mac.Sum(nil)
	// Several during a secret change, separated by spaces: any one valid is enough.
	for _, part := range strings.Split(signatures, " ") {
		version, signature, _ := strings.Cut(part, ",")
		if version != "v1" || signature == "" {
			continue
		}
		given, err := base64.StdEncoding.DecodeString(signature)
		if err != nil || !hmac.Equal(given, expected) {
			continue
		}
		event := new(Event)
		if err := json.Unmarshal(payload, event); err != nil {
			return nil, err
		}
		return event, nil
	}
	return nil, &webhookError{"la firma del webhook no coincide con el secreto"}
}

// DataObject lee data.object de un evento como su objeto: Invoice en invoice.*, Retention en
// retention.*, InvoiceBatch en invoice_batch.*, Balance en balance.low…
//
//	if event.Type == "invoice.stamped" {
//		invoice, err := fiscalisto.DataObject[fiscalisto.Invoice](event)
//	}
func DataObject[T any](event *Event) (*T, error) {
	if event == nil || event.Data.Object == nil {
		return nil, errors.New("fiscalisto: el evento no trae data.object")
	}
	b, err := json.Marshal(event.Data.Object)
	if err != nil {
		return nil, err
	}
	out := new(T)
	if err := json.Unmarshal(b, out); err != nil {
		return nil, err
	}
	return out, nil
}

// headerValue is header.Get, also for an http.Header built from a map of lowercase names (an AWS
// Lambda event's), which Get doesn't find.
func headerValue(header http.Header, name string) string {
	if v := header.Get(name); v != "" {
		return v
	}
	for k, v := range header {
		if len(v) > 0 && strings.EqualFold(k, name) {
			return v[0]
		}
	}
	return ""
}
