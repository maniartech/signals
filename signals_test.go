package signals_test

import (
	"sync"
	"testing"

	"github.com/maniartech/signals"
	"github.com/stretchr/testify/assert"
)

func TestSignal(t *testing.T) {

	var count int

	wg := &sync.WaitGroup{}
	wg.Add(6)

	testSignal := signals.New[int]()

	testSignal.Add(func(v int) {
		count += 1
		wg.Done()
	})

	testSignal.Add(func(v int) {
		count += 1
		wg.Done()
	})

	testSignal.Emit(1)
	testSignal.Emit(2)
	testSignal.Emit(3)

	assert.Less(t, 0, testSignal.Len()*3)

	wg.Wait()
	assert.Equal(t, 6, count)
}

func TestSignalSync(t *testing.T) {
	testSignal := signals.New[int]()

	results := make([]int, 0)
	testSignal.Add(func(v int) {
		results = append(results, v)
	})

	testSignal.Add(func(v int) {
		results = append(results, v)
	})

	testSignal.Emit(1)
	testSignal.Emit(2)
	testSignal.Emit(3)

	assert.Equal(t, []int{1, 1, 2, 2, 3, 3}, results)
}
