package example

import "github.com/maniartech/signals"

// Synchonous signals
var RecordCreated = signals.New[Record]()
var RecordUpdated = signals.New[Record]()
var RecordDeleted = signals.New[Record]()

// Asynchonous signals
var RecordCreatedAsync = signals.NewAsync[Record]()
var RecordUpdatedAsync = signals.NewAsync[Record]()
var RecordDeletedAsync = signals.NewAsync[Record]()
