package postgres_test

import (
	"sync"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestConcurrentDefaultBackendChoicesHaveOneWinner(t *testing.T) {
	store, ctx := newTestStore(t)
	f := newFixture(t, store, ctx)
	other := f.backend
	other.ID = domain.NewBackendInstanceID()
	other.Name = "second"
	if err := store.CreateBackendInstance(ctx, &other, domain.NewUUID(), nil, nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			id := f.backend.ID
			if i%2 == 0 {
				id = other.ID
			}
			if err := store.SetDefaultBackend(ctx, f.owner, id); err != nil {
				t.Errorf("concurrent choice: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	backends, err := store.ListBackendInstances(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, backend := range backends {
		if backend.IsDefault {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("got %d default backends", count)
	}
	if err := store.SetDefaultBackend(ctx, f.owner, ""); err != nil {
		t.Fatal(err)
	}
	backends, err = store.ListBackendInstances(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, backend := range backends {
		if backend.IsDefault {
			t.Fatal("default not cleared")
		}
	}
}
