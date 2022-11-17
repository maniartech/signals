package signals

type base[T any] struct {
	subscribers []keyedListener[T]

	subscribersMap map[string]SignalListener[T]
}

func (s *base[T]) AddListener(listener SignalListener[T], key ...string) int {

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

func (s *base[T]) RemoveListener(key string) int {
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

func (s *base[T]) Reset() {
	s.subscribers = make([]keyedListener[T], 0)
	s.subscribersMap = make(map[string]SignalListener[T])
}

func (s *base[T]) Len() int {
	return len(s.subscribers)
}

func (s *base[T]) IsEmpty() bool {
	return len(s.subscribers) == 0
}
