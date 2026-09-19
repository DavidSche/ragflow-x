package service

import (
	"context"
	"fmt"
	"testing"
)

func TestDeleteDatasetsValidatesBatchContract(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()

	if err := svc.DeleteDatasets(ctx, "tenant", nil); err == nil {
		t.Fatal("empty batch should fail")
	}
	if err := svc.DeleteDatasets(ctx, "tenant", []string{"", "same", "same"}); err == nil {
		t.Fatal("duplicate and empty ids should fail")
	}

	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("dataset-%d", i)
	}
	if err := svc.DeleteDatasets(ctx, "tenant", tooMany); err == nil {
		t.Fatal("batch larger than 100 should fail")
	}
}
