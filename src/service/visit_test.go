package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/CiroLong/shortlink/src/database"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
)

func newVisitSyncerForTest(t *testing.T) (*VisitSyncer, *redis.Client) {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis client: %v", err)
		}
	})

	return &VisitSyncer{
		db:  &database.DB{Redis: client},
		ctx: context.Background(),
	}, client
}

func TestTakeVisitCountDoesNotLoseConcurrentIncrement(t *testing.T) {
	vs, client := newVisitSyncerForTest(t)
	const (
		key          = "visit:test-code"
		initialCount = int64(100)
		iterations   = 100
	)

	for i := 0; i < iterations; i++ {
		if err := client.Set(vs.ctx, key, initialCount, 0).Err(); err != nil {
			t.Fatalf("seed visit count: %v", err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var drained int64
		var drainErr, incrementErr error
		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			drained, drainErr = vs.takeVisitCount(key)
		}()
		go func() {
			defer wg.Done()
			<-start
			incrementErr = client.Incr(vs.ctx, key).Err()
		}()

		close(start)
		wg.Wait()
		if drainErr != nil {
			t.Fatalf("take visit count: %v", drainErr)
		}
		if incrementErr != nil {
			t.Fatalf("increment visit count: %v", incrementErr)
		}

		remaining, err := client.Get(vs.ctx, key).Int64()
		if errors.Is(err, redis.Nil) {
			remaining = 0
		} else if err != nil {
			t.Fatalf("read remaining visit count: %v", err)
		}
		if total := drained + remaining; total != initialCount+1 {
			t.Fatalf("expected %d total visits, got %d", initialCount+1, total)
		}
	}
}

func TestRestoreVisitCountsMergesWithNewVisits(t *testing.T) {
	vs, client := newVisitSyncerForTest(t)
	const key = "visit:test-code"

	if err := client.Set(vs.ctx, key, 5, 0).Err(); err != nil {
		t.Fatalf("seed visit count: %v", err)
	}
	drained, err := vs.takeVisitCount(key)
	if err != nil {
		t.Fatalf("take visit count: %v", err)
	}
	if err := client.IncrBy(vs.ctx, key, 2).Err(); err != nil {
		t.Fatalf("add new visits: %v", err)
	}

	if err := vs.restoreVisitCounts([]pendingVisitCount{{key: key, count: drained}}); err != nil {
		t.Fatalf("restore visit count: %v", err)
	}

	count, err := client.Get(vs.ctx, key).Int64()
	if err != nil {
		t.Fatalf("read restored visit count: %v", err)
	}
	if count != 7 {
		t.Fatalf("expected restored count 7, got %d", count)
	}
}
