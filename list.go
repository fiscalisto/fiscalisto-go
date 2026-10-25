package fiscalisto

import (
	"context"
	"encoding/json"
	"iter"
	"net/url"
)

// Page es una página de una lista: Data y HasMore. Pida la siguiente con Next, o recorra todo
// con el método …AutoPaging del recurso.
type Page[T any] struct {
	Object  string `json:"object"`
	Data    []T    `json:"data"`
	HasMore bool   `json:"has_more"`
	URL     string `json:"url"`

	next func(ctx context.Context, startingAfter string) (*Page[T], error)
}

// Next da la página siguiente, o nil cuando no hay más.
func (p *Page[T]) Next(ctx context.Context) (*Page[T], error) {
	if !p.HasMore || len(p.Data) == 0 || p.next == nil {
		return nil, nil
	}
	return p.next(ctx, idOf(p.Data[len(p.Data)-1]))
}

// idOf: an item's "id", where the next page starts.
func idOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	var x struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &x)
	return x.ID
}

func list[T any](ctx context.Context, c *Client, path string, query any, startingAfter string, opts []RequestOption) (*Page[T], error) {
	q, err := queryOf(query)
	if err != nil {
		return nil, err
	}
	if startingAfter != "" {
		q.Set("starting_after", startingAfter)
	}
	page := new(Page[T])
	if err := c.do(ctx, "GET", path, nil, rawQuery(q), false, page, opts); err != nil {
		return nil, err
	}
	page.next = func(ctx context.Context, after string) (*Page[T], error) {
		return list[T](ctx, c, path, query, after, opts)
	}
	return page, nil
}

func listAll[T any](ctx context.Context, c *Client, path string, query any, opts []RequestOption) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		page, err := list[T](ctx, c, path, query, "", opts)
		for {
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			if page == nil {
				return
			}
			for _, item := range page.Data {
				if !yield(item, nil) {
					return
				}
			}
			page, err = page.Next(ctx)
		}
	}
}

// rawQuery: query values already built, passed through queryOf as they are.
type rawQuery url.Values
