package fiscalisto

import (
	"encoding/json"
	"fmt"
)

// Error es un error de la API, o de la conexión (Type "connection_error"). Decida con Code, que
// es estable (compárelo con las constantes Code…); Message es para mostrarlo a una persona.
//
//	var apiErr *fiscalisto.Error
//	if errors.As(err, &apiErr) && apiErr.Code == fiscalisto.CodeInvoiceRejected {
//		fmt.Println(apiErr.SatCode, apiErr.Message)
//	}
type Error struct {
	// invalid_request_error, authentication_error, permission_error, not_found_error,
	// idempotency_error, rate_limit_error, invoice_error, balance_error, api_error o
	// connection_error.
	Type string `json:"type"`
	// La razón precisa, estable: vea https://fiscalisto.com/docs/api/errores.
	Code string `json:"code"`
	// Qué pasó y cómo corregirlo.
	Message string `json:"message"`
	// El campo que hay que corregir (items.0.unit_price).
	Param string `json:"param,omitempty"`
	// El código del SAT o del PAC cuando ellos lo rechazaron (CFDI40145).
	SatCode string `json:"sat_code,omitempty"`
	// El comprobante rechazado (inv_…), cuando lo hay.
	Invoice string `json:"invoice,omitempty"`
	DocURL  string `json:"doc_url,omitempty"`
	// Búsquelo en Desarrolladores › Solicitudes o mándelo a soporte.
	RequestID string `json:"request_id,omitempty"`
	// El código HTTP (0: la solicitud no llegó).
	StatusCode int `json:"-"`
	// La Idempotency-Key que se mandó: repita con ella para saber si se timbró.
	IdempotencyKey string `json:"-"`
	// El objeto error tal como llegó.
	Raw json.RawMessage `json:"-"`

	cause error
}

func (e *Error) Error() string {
	if e.StatusCode == 0 {
		return fmt.Sprintf("fiscalisto: %s (%s)", e.Message, e.Code)
	}
	return fmt.Sprintf("fiscalisto: %s (%s, HTTP %d)", e.Message, e.Code, e.StatusCode)
}

// Unwrap da la falla de red detrás de un connection_error.
func (e *Error) Unwrap() error { return e.cause }
