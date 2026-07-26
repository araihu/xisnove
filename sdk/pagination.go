package sdk

import (
	"context"
	"errors"
)

var (
	ErrRepeatedCursor  = errors.New("pagination cursor repeated")
	ErrNilPageFetcher  = errors.New("page fetcher is nil")
	ErrNilPageCallback = errors.New("page callback is nil")
)

type Page[T any] struct {
	Items      []T
	NextCursor string
}

type PageFetcher[T any] func(context.Context, string) (Page[T], error)

type PageCallback[T any] func(context.Context, []T) error

func WalkPages[T any](ctx context.Context, fetch PageFetcher[T], callback PageCallback[T]) error {
	if fetch == nil {
		return ErrNilPageFetcher
	}
	if callback == nil {
		return ErrNilPageCallback
	}

	cursor := ""
	seen := map[string]struct{}{cursor: {}}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := fetch(ctx, cursor)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := callback(ctx, page.Items); err != nil {
			return err
		}
		if page.NextCursor == "" {
			return nil
		}
		if _, repeated := seen[page.NextCursor]; repeated {
			return ErrRepeatedCursor
		}
		seen[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
}
