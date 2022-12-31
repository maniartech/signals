package main

import (
	"os"
	"time"

	"github.com/maniartech/signals/example/example"
)

func main() {

	// If the first argument is "-sync" then run the async example
	if len(os.Args) > 1 && os.Args[1] == "-sync" {
		example.RunSync()
	} else {
		example.RunAsync()
	}

	// Wait for a second to let the signals to be processed
	time.Sleep(time.Second)
}
