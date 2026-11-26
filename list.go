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

	next func(ctx context.Context, cursor string) (*Page[T], error)
	// forward: the list was asked with ending_before, so the next page is the newer one, after
	// the first of Data.
	forward bool
}

// Next da la página siguiente, o nil cuando no hay más. Con ending_before, la siguiente es la
// de los más recientes.
func (p *Page[T]) Next(ctx context.Context) (*Page[T], error) {
	if !p.HasMore || len(p.Data) == 0 || p.next == nil {
		return nil, nil
	}
	if p.forward {
		return p.next(ctx, idOf(p.Data[0]))
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

func list[T any](ctx context.Context, c *Client, path string, query any, cursor string, opts []RequestOption) (*Page[T], error) {
	q, err := queryOf(query)
	if err != nil {
		return nil, err
	}
	forward := q.Get("ending_before") != ""
	if cursor != "" {
		if forward {
			q.Set("ending_before", cursor)
		} else {
			q.Set("starting_after", cursor)
		}
	}
	page := new(Page[T])
	if err := c.do(ctx, "GET", path, nil, rawQuery(q), false, page, opts); err != nil {
		return nil, err
	}
	page.forward = forward
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
			for i := range page.Data {
				// ending_before: each page is newest first; read it oldest first.
				item := page.Data[i]
				if page.forward {
					item = page.Data[len(page.Data)-1-i]
				}
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
