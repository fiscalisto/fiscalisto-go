package fiscalisto

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The fixtures every SDK's tests share (sdk/fixtures): the routes and a signed webhook.

func TestEveryRouteHasAMethod(t *testing.T) {
	b, err := os.ReadFile("../fixtures/calls.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls map[string]struct {
		Go string `json:"go"`
	}
	if err := json.Unmarshal(b, &calls); err != nil {
		t.Fatal(err)
	}
	c, _ := NewClient("fk_test_x")
	for route, call := range calls {
		resource, method, _ := strings.Cut(call.Go, ".")
		svc := reflect.ValueOf(c).Elem().FieldByName(resource)
		if !svc.IsValid() || !svc.MethodByName(method).IsValid() {
			t.Errorf("%s: no hay client.%s", route, call.Go)
		}
	}
}

func TestSharedWebhookVector(t *testing.T) {
	b, err := os.ReadFile("../fixtures/webhook.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Secret    string            `json:"secret"`
		Headers   map[string]string `json:"headers"`
		Overlap   string            `json:"overlap_signature"`
		Wrong     string            `json:"wrong_signature"`
		Body      string            `json:"body"`
		Timestamp int64             `json:"timestamp"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	for k, val := range v.Headers {
		h.Set(k, val)
	}
	at := time.Unix(v.Timestamp, 0)
	event, err := VerifyWebhookAt([]byte(v.Body), h, v.Secret, at, DefaultWebhookTolerance)
	if err != nil || event.ID != v.Headers["webhook-id"] {
		t.Fatalf("%v %+v", err, event)
	}
	h.Set("Webhook-Signature", v.Overlap)
	if _, err := VerifyWebhookAt([]byte(v.Body), h, v.Secret, at, DefaultWebhookTolerance); err != nil {
		t.Fatalf("dos firmas: %v", err)
	}
	h.Set("Webhook-Signature", v.Wrong)
	if _, err := VerifyWebhookAt([]byte(v.Body), h, v.Secret, at, DefaultWebhookTolerance); !errors.Is(err, ErrWebhookSignature) {
		t.Fatalf("firma equivocada: %v", err)
	}
}
