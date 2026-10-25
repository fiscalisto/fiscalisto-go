package fiscalisto

import (
	"bytes"
	"encoding/json"
)

// StringOr es un campo que llega (o se manda) como un id ("cus_…") o como el objeto completo,
// según lo que pidió expandir. Lea ID u Object; para mandarlo, use ID(…) o Object(…).
type StringOr[T any] struct {
	ID     string
	Object *T
}

// IDOf manda solo el id.
func IDOf[T any](id string) StringOr[T] { return StringOr[T]{ID: id} }

// ObjectOf manda el objeto completo.
func ObjectOf[T any](v T) StringOr[T] { return StringOr[T]{Object: &v} }

func (s StringOr[T]) MarshalJSON() ([]byte, error) {
	if s.Object != nil {
		return json.Marshal(s.Object)
	}
	return json.Marshal(s.ID)
}

func (s *StringOr[T]) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		s.Object = nil
		return json.Unmarshal(b, &s.ID)
	}
	if bytes.Equal(b, []byte("null")) {
		*s = StringOr[T]{}
		return nil
	}
	v := new(T)
	if err := json.Unmarshal(b, v); err != nil {
		return err
	}
	s.Object = v
	s.ID = idOf(v)
	return nil
}
