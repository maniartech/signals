package example

import (
	"context"
	"fmt"
)

func RunSync() {

	// Add a listener to the RecordCreated signal
	RecordCreatedSync.AddListener(func(ctx context.Context, record Record) {
		fmt.Println("Record created:", record)
	}, "key1") // <- Key is optional useful for removing the listener later

	// Add a listener to the RecordUpdated signal
	RecordUpdatedSync.AddListener(func(ctx context.Context, record Record) {
		fmt.Println("Record updated:", record)
	})

	// Add a listener to the RecordDeleted signal
	RecordDeletedSync.AddListener(func(ctx context.Context, record Record) {
		fmt.Println("Record deleted:", record)
	})

	ctx := context.Background()

	// Emit the RecordCreated signal
	RecordCreatedSync.Emit(ctx, Record{ID: 3, Name: "Record C"})

	// Emit the RecordUpdated signal
	RecordUpdatedSync.Emit(ctx, Record{ID: 2, Name: "Record B"})

	// Emit the RecordDeleted signal
	RecordDeletedSync.Emit(ctx, Record{ID: 1, Name: "Record A"})
}
