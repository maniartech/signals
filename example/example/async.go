package example

import (
	"context"
	"fmt"
)

func RunAsync() {

	// Add a listener to the RecordCreatedAsync signal
	RecordCreatedAsync.AddListener(func(ctx context.Context, record Record) {
		fmt.Println("Record created:", record)
	}, "key1") // <- Key is optional useful for removing the listener later

	// Add a listener to the RecordUpdatedAsync signal
	RecordUpdatedAsync.AddListener(func(ctx context.Context, record Record) {
		fmt.Println("Record updated:", record)
	})

	// Add a listener to the RecordDeleted signal
	RecordDeletedAsync.AddListener(func(ctx context.Context, record Record) {
		fmt.Println("Record deleted:", record)
	})

	ctx := context.Background()

	// Emit the RecordCreatedAsync signal
	RecordCreatedAsync.Emit(ctx, Record{ID: 3, Name: "Record C"})

	// Emit the RecordUpdated signal
	RecordUpdatedAsync.Emit(ctx, Record{ID: 2, Name: "Record B"})

	// Emit the RecordDeleted signal
	RecordDeletedAsync.Emit(ctx, Record{ID: 1, Name: "Record A"})
}
