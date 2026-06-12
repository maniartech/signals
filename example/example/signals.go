package example

import "github.com/maniartech/signals"

// RecordCreated is an asynchronous signal emitted when a record is created.
var RecordCreated = signals.New[Record]()

// RecordUpdated is an asynchronous signal emitted when a record is updated.
var RecordUpdated = signals.New[Record]()

// RecordDeleted is an asynchronous signal emitted when a record is deleted.
var RecordDeleted = signals.New[Record]()

// RecordCreatedSync is a synchronous signal emitted when a record is created.
var RecordCreatedSync = signals.NewSync[Record]()

// RecordUpdatedSync is a synchronous signal emitted when a record is updated.
var RecordUpdatedSync = signals.NewSync[Record]()

// RecordDeletedSync is a synchronous signal emitted when a record is deleted.
var RecordDeletedSync = signals.NewSync[Record]()
