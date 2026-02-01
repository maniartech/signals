package signals

import "testing"

func TestSyncSubscribersPoolType(t *testing.T) {
	poolVal := syncSubscribersPool.Get()
	if poolVal == nil {
		t.Fatal("Expected non-nil pool value")
	}
	if _, ok := poolVal.([]keyedListener[int]); !ok {
		t.Fatalf("Expected pool to return []keyedListener[int], got %T", poolVal)
	}
	syncSubscribersPool.Put(poolVal)
}

func TestEmitTaskPoolType(t *testing.T) {
	poolVal := emitTaskPool.Get()
	if poolVal == nil {
		t.Fatal("Expected non-nil pool value")
	}
	if _, ok := poolVal.(*emitTask[int]); !ok {
		t.Fatalf("Expected pool to return *emitTask[int], got %T", poolVal)
	}
	emitTaskPool.Put(poolVal)
}

func TestAsyncSubscribersPoolType(t *testing.T) {
	poolVal := asyncSubscribersPool.Get()
	if poolVal == nil {
		t.Fatal("Expected non-nil pool value")
	}
	if _, ok := poolVal.([]keyedListener[int]); !ok {
		t.Fatalf("Expected pool to return []keyedListener[int], got %T", poolVal)
	}
	asyncSubscribersPool.Put(poolVal)
}
