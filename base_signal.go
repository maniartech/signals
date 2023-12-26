package signals

import "context"

type keyedListener[T any] struct {
	key      string
	listener SignalListener[T]
}

// BaseSignal provides the base implementation of the Signal interface.
//
type BaseSignal[T any] struct {
	subscribers []keyedListener[T]

	subscribersMap map[string]SignalListener[T]
}

func (s *BaseSignal[T]) AddListener(listener SignalListener[T], key ...string) int {

	if len(key) > 0 {
		if _, ok := s.subscribersMap[key[0]]; ok {
			return -1
		}
		s.subscribersMap[key[0]] = listener
		s.subscribers = append(s.subscribers, keyedListener[T]{
			key:      key[0],
			listener: listener,
		})
	} else {
		s.subscribers = append(s.subscribers, keyedListener[T]{
			listener: listener,
		})
	}

	return len(s.subscribers)
}

func (s *BaseSignal[T]) RemoveListener(key string) int {
	if _, ok := s.subscribersMap[key]; ok {
		delete(s.subscribersMap, key)

		for i, sub := range s.subscribers {
			if sub.key == key {
				s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
				break
			}
		}
		return len(s.subscribers)
	}

	return -1
}

func (s *BaseSignal[T]) Reset() {
	s.subscribers = make([]keyedListener[T], 0)
	s.subscribersMap = make(map[string]SignalListener[T])
}

func (s *BaseSignal[T]) Len() int {
	return len(s.subscribers)
}

func (s *BaseSignal[T]) IsEmpty() bool {
	return len(s.subscribers) == 0
}

// Emit notifies all subscribers of the signal and passes the payload.
// If the context has a deadline or cancellable property, the listeners
// must respect it. If the signal is async, the listeners are called
// in a separate goroutine.
func (s *BaseSignal[T]) Emit(ctx context.Context, payload T) {
	panic("implement me in derived type")
}
