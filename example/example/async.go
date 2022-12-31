package example

import (
	"context"
	"fmt"
)

func RunAsync() {

	// Add a listener to the RecordCreatedAsync signal
	RecordCreated.AddListener(func(ctx context.Context, record Record) {
		fmt.Println("Record created:", record)
	}, "key1") // <- Key is optional useful for removing the listener later

	// Add a listener to the RecordUpdatedAsync signal
	RecordUpdated.AddListener(func(ctx context.Context, record Record) {
		fmt.Println("Record updated:", record)
	})

	// Add a listener to the RecordDeleted signal
	RecordDeleted.AddListener(func(ctx context.Context, record Record) {
		fmt.Println("Record deleted:", record)
	})

	ctx := context.Background()

	// Emit the RecordCreatedAsync signal
	RecordCreated.Emit(ctx, Record{ID: 3, Name: "Record C"})

	// Emit the RecordUpdated signal
	RecordUpdated.Emit(ctx, Record{ID: 2, Name: "Record B"})

	// Emit the RecordDeleted signal
	RecordDeleted.Emit(ctx, Record{ID: 1, Name: "Record A"})
}
