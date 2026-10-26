package fiscalisto

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient("fk_test_abc", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestKeys(t *testing.T) {
	t.Setenv("FISCALISTO_API_KEY", "")
	if _, err := NewClient(""); err == nil {
		t.Fatal("sin llave debe fallar")
	}
	if _, err := NewClient("whsec_x"); err == nil || !strings.Contains(err.Error(), "fk_live_") {
		t.Fatalf("una llave que no es fk_: %v", err)
	}
	t.Setenv("FISCALISTO_API_KEY", "fk_live_x")
	c, err := NewClient("")
	if err != nil || !c.Livemode() {
		t.Fatalf("de la variable: %v", err)
	}
}

func TestCreateSendsJSONAndIdempotencyKey(t *testing.T) {
	var keys []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fk_test_abc" || !strings.HasPrefix(r.Header.Get("User-Agent"), "fiscalisto-go/") || r.Header.Get("Fiscalisto-Version") != APIVersion {
			t.Errorf("encabezados: %v", r.Header)
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if len(keys) == 1 {
			w.WriteHeader(502)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["customer"] != "cus_1" || body["payment_form"] != "03" || body["series"] != nil {
			t.Errorf("cuerpo: %v", body)
		}
		w.Header().Set("Request-Id", "req_1")
		_, _ = io.WriteString(w, `{"id":"inv_1","object":"invoice","status":"valid","customer":{"tax_id":"XAXX010101000"},"currency":"MXN","items":[],"total":116.0,"uuid":null}`)
	})
	inv, err := c.Invoices.Create(context.Background(), &InvoiceCreate{
		Customer:    IDOf[InvoiceCreateCustomer]("cus_1"),
		Items:       []InvoiceCreateItem{},
		PaymentForm: String("03"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if inv.ID != "inv_1" || inv.Total == nil || *inv.Total != 116 || inv.UUID != nil {
		t.Fatalf("respuesta: %+v", inv)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("la misma Idempotency-Key en el reintento: %v", keys)
	}
}

func TestErrors(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Request-Id", "req_9")
		w.WriteHeader(422)
		_, _ = io.WriteString(w, `{"error":{"type":"invoice_error","code":"invoice_rejected","message":"El SAT no la aceptó.","sat_code":"CFDI40145","doc_url":"https://fiscalisto.com/docs/api/errores#invoice_rejected","request_id":"req_9"}}`)
	})
	_, err := c.Invoices.Get(context.Background(), "inv/1", nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("no es *Error: %v", err)
	}
	if apiErr.Code != CodeInvoiceRejected || apiErr.SatCode != "CFDI40145" || apiErr.StatusCode != 422 || apiErr.RequestID != "req_9" {
		t.Fatalf("%+v", apiErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("un 422 no se repite: %d", calls.Load())
	}
}

func TestPostRetriesWithTheSameKey(t *testing.T) {
	var calls atomic.Int32
	keys := map[string]bool{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		keys[r.Header.Get("Idempotency-Key")] = true
		if calls.Add(1) < 3 {
			w.WriteHeader(500)
			return
		}
		_, _ = io.WriteString(w, `{"id":"cus_1"}`)
	})
	if _, err := c.Customers.Update(context.Background(), "cus_1", &UpdateCustomerParams{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || len(keys) != 1 || keys[""] {
		t.Fatalf("%d llamadas, llaves %v", calls.Load(), keys)
	}
}

func TestPostWithoutKeyIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("sin idempotent no lleva llave")
		}
		w.WriteHeader(500)
	})
	err := c.do(context.Background(), "POST", "/v1/customers", map[string]any{}, nil, false, nil, nil)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 500 || calls.Load() != 1 {
		t.Fatalf("%v, %d llamadas", err, calls.Load())
	}
}

func TestRateLimitHonorsRetryAfter(t *testing.T) {
	var waited []time.Duration
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"error":{"type":"rate_limit_error","code":"rate_limited","message":"x"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"cus_1","object":"customer"}`)
	})
	c.sleep = func(_ context.Context, d time.Duration) error { waited = append(waited, d); return nil }
	if _, err := c.Customers.Get(context.Background(), "cus_1"); err != nil {
		t.Fatal(err)
	}
	if len(waited) != 1 || waited[0] != 3*time.Second {
		t.Fatalf("esperó %v", waited)
	}
}

func TestListAutoPaging(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("limit") != "2" || q.Get("status") != "valid" {
			t.Errorf("query: %v", q)
		}
		switch q.Get("starting_after") {
		case "":
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"inv_1"},{"id":"inv_2"}],"has_more":true,"url":"/v1/invoices"}`)
		case "inv_2":
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"inv_3"}],"has_more":false,"url":"/v1/invoices"}`)
		default:
			t.Errorf("starting_after: %s", q.Get("starting_after"))
		}
	})
	params := &ListInvoicesParams{Limit: Int64(2), Status: String("valid")}
	var ids []string
	for inv, err := range c.Invoices.ListAutoPaging(context.Background(), params) {
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, inv.ID)
	}
	if strings.Join(ids, ",") != "inv_1,inv_2,inv_3" {
		t.Fatalf("ids: %v", ids)
	}
	page, err := c.Invoices.List(context.Background(), params)
	if err != nil || len(page.Data) != 2 || !page.HasMore {
		t.Fatalf("%v %+v", err, page)
	}
}

func TestBytes(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/invoices/inv_1/pdf" {
			t.Errorf("ruta: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("%PDF-1.7"))
	})
	b, err := c.Invoices.PDF(context.Background(), "inv_1", nil)
	if err != nil || string(b) != "%PDF-1.7" {
		t.Fatalf("%v %q", err, b)
	}
}

func TestConnectionError(t *testing.T) {
	c, _ := NewClient("fk_test_abc", WithBaseURL("http://127.0.0.1:1"), WithMaxRetries(0))
	_, err := c.Customers.Get(context.Background(), "cus_1")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Type != "connection_error" || apiErr.Code != CodeConnectionFailed {
		t.Fatalf("%v", err)
	}
}

func sign(secret, id string, ts int64, body []byte) string {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + strconv.FormatInt(ts, 10) + "."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhook(t *testing.T) {
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	body := []byte(`{"id":"evt_1","object":"event","type":"invoice.stamped","data":{"object":{"id":"inv_1","object":"invoice","status":"valid"}}}`)
	now := time.Unix(1_800_000_000, 0)
	h := http.Header{}
	h.Set("webhook-id", "msg_1")
	h.Set("webhook-timestamp", strconv.FormatInt(now.Unix(), 10))
	h.Set("webhook-signature", "v1,bm90IGl0 "+sign(secret, "msg_1", now.Unix(), body))
	event, err := VerifyWebhookAt(body, h, secret, now, DefaultWebhookTolerance)
	if err != nil || event.Type != "invoice.stamped" {
		t.Fatalf("%v %+v", err, event)
	}
	inv, err := DataObject[Invoice](event)
	if err != nil || inv.ID != "inv_1" || inv.Status != "valid" {
		t.Fatalf("%v %+v", err, inv)
	}
	if _, err := VerifyWebhookAt(append(body, ' '), h, secret, now, DefaultWebhookTolerance); !errors.Is(err, ErrWebhookSignature) {
		t.Fatalf("otro cuerpo: %v", err)
	}
	if _, err := VerifyWebhookAt(body, h, secret, now.Add(10*time.Minute), DefaultWebhookTolerance); !errors.Is(err, ErrWebhookSignature) {
		t.Fatalf("fuera de tiempo: %v", err)
	}
	// An http.Header made from a map of lowercase names (an AWS Lambda event's), not with Set.
	lower := http.Header{}
	for k, v := range h {
		lower[strings.ToLower(k)] = v
	}
	if _, err := VerifyWebhookAt(body, lower, secret, now, DefaultWebhookTolerance); err != nil {
		t.Fatalf("encabezados en minúsculas: %v", err)
	}
}

func TestStringOr(t *testing.T) {
	var v struct {
		C StringOr[Customer] `json:"c"`
		D StringOr[Customer] `json:"d"`
	}
	if err := json.Unmarshal([]byte(`{"c":"cus_1","d":{"id":"cus_2","object":"customer"}}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.C.ID != "cus_1" || v.C.Object != nil || v.D.ID != "cus_2" || v.D.Object == nil {
		t.Fatalf("%+v", v)
	}
	b, _ := json.Marshal(IDOf[Customer]("cus_3"))
	if string(b) != `"cus_3"` {
		t.Fatal(string(b))
	}
}
