// Package fiscalisto es el SDK de Go de la API de facturación de Fiscalisto: timbrar CFDI 4.0,
// cancelarlos, recibir webhooks.
//
//	client, err := fiscalisto.NewClient("fk_test_…")
//	invoice, err := client.Invoices.Create(ctx, &fiscalisto.InvoiceCreate{…})
//
// Cada recurso es un campo de Client (client.Invoices, client.Customers…). Los campos opcionales
// de una solicitud son apuntadores: fiscalisto.String("PUE"), fiscalisto.Int64(10). Lo que timbra
// lleva una Idempotency-Key automática y se reintenta solo cuando es seguro. Documentación:
// https://fiscalisto.com/docs/api/sdk.
package fiscalisto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Version del SDK, en el User-Agent.
const Version = "0.1.3"

// APIVersion es la versión de la API para la que se hizo este SDK (cabecera Fiscalisto-Version).
const APIVersion = "2026-10-24"

// DefaultBaseURL es la dirección de la API.
const DefaultBaseURL = "https://api.fiscalisto.com"

// Client llama a la API con una llave. Úselo desde varias goroutines: no guarda estado entre
// llamadas.
type Client struct {
	services
	key        string
	baseURL    string
	apiVersion string
	language   string
	http       *http.Client
	maxRetries int
	timeout    time.Duration
	sleep      func(context.Context, time.Duration) error
}

// Option configura un Client en NewClient.
type Option func(*Client)

// WithBaseURL usa otra dirección de la API (un proxy, pruebas).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithAPIVersion pide otra versión de la API (Fiscalisto-Version); con "" se usa la fijada en
// la cuenta.
func WithAPIVersion(v string) Option { return func(c *Client) { c.apiVersion = v } }

// WithLanguage con "en" pide los mensajes de error en inglés (Accept-Language).
func WithLanguage(lang string) Option { return func(c *Client) { c.language = lang } }

// WithHTTPClient usa su propio *http.Client (transporte, proxy, trazas).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithMaxRetries cambia cuántas veces se repite una solicitud segura que falló (2 por omisión).
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// WithTimeout cambia cuánto se espera cada respuesta (80 s por omisión: el PAC puede tardar).
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.timeout = d } }

// NewClient crea un cliente con su llave fk_test_… o fk_live_…; con "" usa la variable
// FISCALISTO_API_KEY.
func NewClient(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" {
		apiKey = os.Getenv("FISCALISTO_API_KEY")
	}
	if apiKey == "" {
		return nil, errors.New(`fiscalisto: falta la llave de la API: NewClient("fk_test_…") o la variable FISCALISTO_API_KEY. Créela en Desarrolladores › Llaves`)
	}
	if !strings.HasPrefix(apiKey, "fk_live_") && !strings.HasPrefix(apiKey, "fk_test_") {
		return nil, errors.New("fiscalisto: la llave de la API empieza con fk_live_ o fk_test_ (un secreto whsec_ es para verificar webhooks)")
	}
	c := &Client{
		key:        apiKey,
		baseURL:    DefaultBaseURL,
		apiVersion: APIVersion,
		http:       http.DefaultClient,
		maxRetries: 2,
		timeout:    80 * time.Second,
		sleep:      sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	c.services.init(c)
	return c, nil
}

// Livemode es true con una llave fk_live_ (producción), false con fk_test_ (pruebas).
func (c *Client) Livemode() bool { return strings.HasPrefix(c.key, "fk_live_") }

// RequestOption cambia una sola llamada.
type RequestOption func(*requestConfig)

type requestConfig struct {
	idempotencyKey string
	timeout        time.Duration
	maxRetries     *int
}

// WithIdempotencyKey usa su propia Idempotency-Key (la de su pedido, por ejemplo): repetir la
// llamada con la misma llave devuelve el mismo comprobante, nunca uno nuevo.
func WithIdempotencyKey(key string) RequestOption {
	return func(r *requestConfig) { r.idempotencyKey = key }
}

// WithRequestTimeout cambia la espera de esta llamada.
func WithRequestTimeout(d time.Duration) RequestOption {
	return func(r *requestConfig) { r.timeout = d }
}

// WithRequestMaxRetries cambia los reintentos de esta llamada.
func WithRequestMaxRetries(n int) RequestOption {
	return func(r *requestConfig) { r.maxRetries = &n }
}

// String, Int64, Float64 y Bool dan el apuntador de un valor, para los campos opcionales.
func String(v string) *string { return &v }

// Int64: vea String.
func Int64(v int64) *int64 { return &v }

// Float64: vea String.
func Float64(v float64) *float64 { return &v }

// Bool: vea String.
func Bool(v bool) *bool { return &v }

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// backoff: 0.5 s, 1 s, 2 s… hasta 8 s, con algo de azar para no llegar todos juntos.
func backoff(attempt int) time.Duration {
	d := min(8.0, 0.5*float64(int(1)<<attempt))
	return time.Duration(d * (0.75 + rand.Float64()/2) * float64(time.Second))
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	return r.Kind() == reflect.Pointer && r.IsNil()
}

// queryOf: the params' fields as the URL's query (they are all scalars).
func queryOf(params any) (url.Values, error) {
	if r, ok := params.(rawQuery); ok {
		return url.Values(r), nil
	}
	q := url.Values{}
	if isNil(params) {
		return q, nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		return nil, err
	}
	for k, v := range m {
		switch x := v.(type) {
		case nil:
		case string:
			q.Set(k, x)
		case bool:
			q.Set(k, strconv.FormatBool(x))
		case json.Number:
			q.Set(k, x.String())
		default:
			return nil, fmt.Errorf("fiscalisto: %s no va en la dirección", k)
		}
	}
	return q, nil
}

func randomKey() string {
	var b [16]byte
	for i := range b {
		b[i] = byte(rand.UintN(256))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// raw does one call and returns the answer's bytes, or an *Error.
func (c *Client) raw(ctx context.Context, method, path string, body, query any, idempotent bool, opts []RequestOption) ([]byte, error) {
	b, _, _, err := c.call(ctx, method, path, body, query, idempotent, opts)
	return b, err
}

// call is raw plus the success's status and Request-Id, for an answer that turns out unreadable.
func (c *Client) call(ctx context.Context, method, path string, body, query any, idempotent bool, opts []RequestOption) ([]byte, int, string, error) {
	cfg := requestConfig{timeout: c.timeout}
	for _, o := range opts {
		o(&cfg)
	}
	retries := c.maxRetries
	if cfg.maxRetries != nil {
		retries = *cfg.maxRetries
	}
	q, err := queryOf(query)
	if err != nil {
		return nil, 0, "", err
	}
	target := c.baseURL + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	var data []byte
	if method == http.MethodPost {
		if isNil(body) {
			data = []byte("{}")
		} else if data, err = json.Marshal(body); err != nil {
			return nil, 0, "", err
		}
	}
	key := ""
	if idempotent {
		key = cfg.idempotencyKey
		if key == "" {
			key = randomKey()
		}
	}
	// Only what can't do something twice is repeated after a failure we can't see through:
	// reads, deletes and requests with an Idempotency-Key. A 429 was never processed.
	safe := method == http.MethodGet || method == http.MethodDelete || key != ""

	for attempt := 0; ; attempt++ {
		status, header, answer, err := c.send(ctx, method, target, data, key, cfg.timeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, "", ctx.Err()
			}
			if safe && attempt < retries {
				if err := c.sleep(ctx, backoff(attempt)); err != nil {
					return nil, 0, "", err
				}
				continue
			}
			return nil, 0, "", connectionError(err, key, cfg.timeout)
		}
		if status >= 200 && status < 300 {
			return answer, status, header.Get("Request-Id"), nil
		}
		e := &Error{StatusCode: status, RequestID: header.Get("Request-Id"), IdempotencyKey: key}
		var envelope struct {
			Error json.RawMessage `json:"error"`
		}
		if json.Unmarshal(answer, &envelope) == nil && len(envelope.Error) > 0 {
			_ = json.Unmarshal(envelope.Error, e)
			e.Raw = envelope.Error
			if e.RequestID == "" {
				e.RequestID = header.Get("Request-Id")
			}
		}
		if e.Type == "" {
			e.Type = "api_error"
		}
		if e.Code == "" {
			e.Code = "unknown"
		}
		if e.Message == "" {
			e.Message = "Error de la API de Fiscalisto"
		}
		retryAfter, _ := strconv.ParseFloat(header.Get("Retry-After"), 64)
		retryable := status == http.StatusTooManyRequests || e.Code == CodeRequestInProgress ||
			(safe && status >= 500)
		if retryable && attempt < retries && retryAfter <= 60 {
			wait := backoff(attempt)
			if retryAfter > 0 {
				wait = time.Duration(retryAfter * float64(time.Second))
			}
			if err := c.sleep(ctx, wait); err != nil {
				return nil, 0, "", err
			}
			continue
		}
		return nil, 0, "", e
	}
}

func (c *Client) send(ctx context.Context, method, target string, data []byte, key string, timeout time.Duration) (int, http.Header, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if data != nil {
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("User-Agent", "fiscalisto-go/"+Version)
	req.Header.Set("Accept", "application/json")
	if c.apiVersion != "" {
		req.Header.Set("Fiscalisto-Version", c.apiVersion)
	}
	if c.language != "" {
		req.Header.Set("Accept-Language", c.language)
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer res.Body.Close()
	answer, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, nil, nil, err
	}
	return res.StatusCode, res.Header, answer, nil
}

func connectionError(err error, key string, timeout time.Duration) *Error {
	var netErr net.Error
	timedOut := errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
	e := &Error{Type: "connection_error", Code: CodeConnectionFailed, IdempotencyKey: key, cause: err,
		Message: "No se pudo conectar con la API: " + err.Error()}
	if timedOut {
		e.Code = CodeTimeout
		e.Message = fmt.Sprintf("La API no contestó en %g s.", timeout.Seconds())
		if key != "" {
			e.Message += " Repita con la misma Idempotency-Key (" + key + ") para saber si se timbró."
		}
	}
	return e
}

// do does one call and reads its JSON answer into out.
func (c *Client) do(ctx context.Context, method, path string, body, query any, idempotent bool, out any, opts []RequestOption) error {
	b, status, requestID, err := c.call(ctx, method, path, body, query, idempotent, opts)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		var syntax *json.SyntaxError
		if !errors.As(err, &syntax) && len(bytes.TrimSpace(b)) > 0 {
			return fmt.Errorf("fiscalisto: la respuesta no se pudo leer: %w", err)
		}
		// A proxy's or a network portal's page answered instead of the API.
		text := strings.Join(strings.Fields(string(b)), " ")
		if len(text) > 80 {
			text = text[:80]
		}
		if text == "" {
			text = "vacía"
		}
		return &Error{Type: "api_error", Code: CodeInvalidResponse, StatusCode: status, RequestID: requestID, cause: err,
			Message: fmt.Sprintf("La respuesta (%d) no es de la API: %s. ¿Hay un proxy o un portal de por medio?", status, text)}
	}
	return nil
}
