package example

import "github.com/maniartech/signals"

// Asynchonous signals
var RecordCreated = signals.New[Record]()
var RecordUpdated = signals.New[Record]()
var RecordDeleted = signals.New[Record]()

// Synchonous signals
var RecordCreatedSync = signals.NewSync[Record]()
var RecordUpdatedSync = signals.NewSync[Record]()
var RecordDeletedSync = signals.NewSync[Record]()
