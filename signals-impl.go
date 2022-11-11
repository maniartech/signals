package signals

type signal[T any] struct {
	base[T]

	async bool
}

func (s *signal[T]) Emit(payload T) {
	for _, sub := range s.subscribers {
		if s.async {
			go sub.listener(payload)
		} else {
			sub.listener(payload)
		}
	}
}
