package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestClaimJobsAtomicUnderConcurrency(t *testing.T) {
	s := newJobStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := s.EnqueueJob(ctx, &model.Job{TenantID: "t1", Key: "concurrent", Kind: model.JobKindDocumentSync, RunAfter: now}); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var wg sync.WaitGroup
	claimed := make(chan int, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs, err := s.ClaimJobs(ctx, 10, now)
			if err != nil {
				t.Error(err)
				return
			}
			claimed <- len(jobs)
		}()
	}
	wg.Wait()
	close(claimed)
	var total int
	for n := range claimed {
		total += n
	}
	if total != 1 {
		t.Fatalf("expected one claim across workers, got %d", total)
	}
}
