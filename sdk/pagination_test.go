package sdk_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/araihu/xisnove/sdk"
)

func TestWalkPagesPassesOpaqueCursorAndStopsOnEmpty(t *testing.T) {
	wantCursors := []string{"", "opaque/one==", "opaque.two"}
	pages := map[string]sdk.Page[int]{
		"":             {Items: []int{1, 2}, NextCursor: "opaque/one=="},
		"opaque/one==": {Items: []int{3}, NextCursor: "opaque.two"},
		"opaque.two":   {Items: []int{4}, NextCursor: ""},
	}
	var cursors []string
	var items []int
	err := sdk.WalkPages(context.Background(),
		func(_ context.Context, cursor string) (sdk.Page[int], error) {
			cursors = append(cursors, cursor)
			return pages[cursor], nil
		},
		func(_ context.Context, page []int) error {
			items = append(items, page...)
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cursors, wantCursors) {
		t.Fatalf("cursors = %#v, want unchanged %#v", cursors, wantCursors)
	}
	if !reflect.DeepEqual(items, []int{1, 2, 3, 4}) {
		t.Fatalf("items = %#v", items)
	}
}

func TestWalkPagesPropagatesFetcherCallbackAndContextErrors(t *testing.T) {
	fetchErr := errors.New("fetch failed")
	if err := sdk.WalkPages(context.Background(),
		func(context.Context, string) (sdk.Page[int], error) { return sdk.Page[int]{}, fetchErr },
		func(context.Context, []int) error { return nil },
	); !errors.Is(err, fetchErr) {
		t.Fatalf("fetch error = %v", err)
	}

	callbackErr := errors.New("callback failed")
	if err := sdk.WalkPages(context.Background(),
		func(context.Context, string) (sdk.Page[int], error) { return sdk.Page[int]{Items: []int{1}}, nil },
		func(context.Context, []int) error { return callbackErr },
	); !errors.Is(err, callbackErr) {
		t.Fatalf("callback error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := sdk.WalkPages(ctx,
		func(context.Context, string) (sdk.Page[int], error) { called = true; return sdk.Page[int]{}, nil },
		func(context.Context, []int) error { called = true; return nil },
	); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("context error = %v, called = %t", err, called)
	}
}

func TestWalkPagesRejectsRepeatedCursorAndSelfLoop(t *testing.T) {
	for name, next := range map[string][]string{
		"self loop": {"same", "same"},
		"cycle":     {"one", "two", "one"},
	} {
		t.Run(name, func(t *testing.T) {
			call := 0
			err := sdk.WalkPages(context.Background(),
				func(context.Context, string) (sdk.Page[int], error) {
					cursor := next[call]
					call++
					return sdk.Page[int]{NextCursor: cursor}, nil
				},
				func(context.Context, []int) error { return nil },
			)
			if !errors.Is(err, sdk.ErrRepeatedCursor) {
				t.Fatalf("WalkPages() error = %v, want ErrRepeatedCursor", err)
			}
		})
	}
}

func TestWalkPagesObservesCancellationBetweenPages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fetches := 0
	err := sdk.WalkPages(ctx,
		func(context.Context, string) (sdk.Page[int], error) {
			fetches++
			return sdk.Page[int]{Items: []int{1}, NextCursor: "next"}, nil
		},
		func(context.Context, []int) error {
			cancel()
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) || fetches != 1 {
		t.Fatalf("WalkPages() = %v after %d fetches", err, fetches)
	}
}

func TestWalkPagesObservesCancellationAfterTerminalPageCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	err := sdk.WalkPages(ctx,
		func(context.Context, string) (sdk.Page[int], error) {
			return sdk.Page[int]{Items: []int{1}, NextCursor: ""}, nil
		},
		func(context.Context, []int) error {
			cancel()
			return nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WalkPages() = %v, want terminal callback cancellation", err)
	}
}
